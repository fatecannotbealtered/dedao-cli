package dedao

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// `daily` answers "what is new in the courses I own since I last asked". That
// needs somewhere to remember what "last time" was, so it keeps a checkpoint
// file of the article ids already seen.
//
// The first run over a course records what is there and reports nothing new.
// That is deliberate: without it, the first run would report a subscriber's
// entire back catalogue as today's updates. `--include-existing` asks for the
// back catalogue explicitly, and `baseline_created` says when a baseline was
// taken so a caller can tell "nothing new" from "nothing known yet".

// checkpointCourse is one course's remembered state.
type checkpointCourse struct {
	Seen []string `json:"seen"`
}

// checkpoint is the file `daily` keeps between runs.
type checkpoint struct {
	Courses map[string]checkpointCourse `json:"courses"`
}

// checkpointLimit caps how many article ids are remembered per course. A course
// publishes daily, so a few hundred ids covers any realistic gap between runs
// without letting the file grow forever.
const checkpointLimit = 500

// CheckpointPath resolves where the checkpoint lives.
func (c *Client) CheckpointPath(override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	return filepath.Join(c.stateDir, "checkpoint.json")
}

// loadCheckpoint reads the checkpoint, treating a missing file as a first run.
//
// An unreadable file is an error rather than a fresh start: silently starting
// over would report the whole back catalogue as new.
func loadCheckpoint(path string) (*checkpoint, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &checkpoint{Courses: map[string]checkpointCourse{}}, nil
	}
	if err != nil {
		return nil, &APIError{Message: "the checkpoint is unreadable: " + path}
	}
	var value checkpoint
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, &APIError{Message: "the checkpoint is invalid: " + path}
	}
	if value.Courses == nil {
		value.Courses = map[string]checkpointCourse{}
	}
	return &value, nil
}

// saveCheckpoint writes the checkpoint atomically, so an interrupted run cannot
// leave a half-written file that the next run would reject.
func saveCheckpoint(path string, value *checkpoint) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return &APIError{Message: "could not create the checkpoint directory"}
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return &APIError{Message: "could not encode the checkpoint"}
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".")
	if err != nil {
		return &APIError{Message: "could not write the checkpoint"}
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return &APIError{Message: "could not write the checkpoint"}
	}
	if err := temporary.Close(); err != nil {
		return &APIError{Message: "could not write the checkpoint"}
	}
	// The checkpoint names what the account owns, so it is kept private.
	_ = os.Chmod(name, 0o600)
	if err := os.Rename(name, path); err != nil {
		return &APIError{Message: "could not replace the checkpoint"}
	}
	return nil
}

// ownedCourses walks the owned-course library to its end.
func (c *Client) ownedCourses(ctx context.Context) ([]map[string]any, error) {
	var courses []map[string]any
	for page := 1; ; page++ {
		value, err := c.Library(ctx, "bauhinia", page, 6)
		if err != nil {
			return nil, err
		}
		result, _ := value.(map[string]any)
		for _, raw := range asList(result["list"]) {
			if course, ok := raw.(map[string]any); ok {
				courses = append(courses, course)
			}
		}
		if !truthyValue(result["is_more"]) {
			return courses, nil
		}
	}
}

// courseArticlesUntilKnown reads a course's articles newest-first, stopping as
// soon as it reaches one already seen.
//
// The cursor is checked for repeating itself: the upstream returns the same
// page when handed a cursor it does not recognize, which would otherwise loop.
func (c *Client) courseArticlesUntilKnown(
	ctx context.Context,
	courseEnid string,
	known map[string]bool,
) ([]map[string]any, error) {
	const pageSize = 30
	var articles []map[string]any
	maxID, maxOrderNum := 0, 0
	cursors := map[[2]int]bool{}
	for {
		value, err := c.CourseArticles(ctx, courseEnid, pageSize, maxID, maxOrderNum, true)
		if err != nil {
			return nil, err
		}
		result, _ := value.(map[string]any)
		page := asList(result["article_list"])
		reachedKnown := false
		for _, raw := range page {
			article, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			articles = append(articles, article)
			if known[asString(article["enid"])] {
				reachedKnown = true
			}
		}
		if len(page) < pageSize || reachedKnown {
			return articles, nil
		}
		last, _ := page[len(page)-1].(map[string]any)
		id, _ := safeInt(last["id"])
		order, _ := safeInt(last["order_num"])
		cursor := [2]int{id, order}
		if cursor == [2]int{0, 0} || cursors[cursor] {
			return articles, nil
		}
		cursors[cursor] = true
		maxID, maxOrderNum = id, order
	}
}

// DailyOptions selects what one `daily` run does.
type DailyOptions struct {
	CheckpointPath  string
	IncludeExisting bool
	WithContent     bool
}

// Daily collects the articles that have appeared in owned courses since the
// last run.
func (c *Client) Daily(ctx context.Context, options DailyOptions) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	path := c.CheckpointPath(options.CheckpointPath)
	state, err := loadCheckpoint(path)
	if err != nil {
		return nil, err
	}

	courses, err := c.ownedCourses(ctx)
	if err != nil {
		return nil, err
	}

	updates := []any{}
	baselineCreated := false
	for _, course := range courses {
		courseEnid := asString(course["enid"])
		if courseEnid == "" {
			continue
		}
		previous, initialized := state.Courses[courseEnid]
		known := map[string]bool{}
		for _, enid := range previous.Seen {
			known[enid] = true
		}

		articles, err := c.courseArticlesUntilKnown(ctx, courseEnid, known)
		if err != nil {
			return nil, err
		}

		var fresh []map[string]any
		if !initialized && !options.IncludeExisting {
			// First sight of this course: record what is there and report
			// nothing, so the run does not read as "today's news".
			baselineCreated = true
		} else {
			for _, article := range articles {
				if !known[asString(article["enid"])] {
					fresh = append(fresh, article)
				}
			}
		}

		for _, article := range fresh {
			item := map[string]any{
				"course_enid":  courseEnid,
				"course_title": course["title"],
				"article":      Sanitize(article),
			}
			if options.WithContent {
				content, err := c.ArticleContent(ctx, asString(article["enid"]), "text")
				if err != nil {
					return nil, err
				}
				item["text"] = content.Text
			}
			updates = append(updates, item)
		}

		seen := make([]string, 0, len(articles)+len(previous.Seen))
		for _, article := range articles {
			if enid := asString(article["enid"]); enid != "" {
				seen = append(seen, enid)
			}
		}
		seen = append(seen, previous.Seen...)
		state.Courses[courseEnid] = checkpointCourse{Seen: dedupeStrings(seen, checkpointLimit)}
	}

	// Oldest first: a caller reading the list in order reads the course in the
	// order it was published.
	sort.SliceStable(updates, func(i, j int) bool {
		return dailyPublishTime(updates[i]) < dailyPublishTime(updates[j])
	})
	if err := saveCheckpoint(path, state); err != nil {
		return nil, err
	}
	return map[string]any{
		"baseline_created": baselineCreated,
		"update_count":     len(updates),
		"updates":          updates,
		"checkpoint":       path,
	}, nil
}

// dailyPublishTime reads one update's publication time for ordering.
func dailyPublishTime(value any) int {
	item, _ := value.(map[string]any)
	article, _ := item["article"].(map[string]any)
	published, _ := safeInt(article["publish_time"])
	return published
}

// dedupeStrings keeps the first occurrence of each value, up to a limit.
func dedupeStrings(values []string, limit int) []string {
	seen := map[string]bool{}
	kept := []string{}
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		kept = append(kept, value)
		if len(kept) == limit {
			break
		}
	}
	return kept
}

// asList reads a JSON array field, or nothing when the field is absent.
func asList(value any) []any {
	list, _ := value.([]any)
	return list
}

// asString reads a JSON string field, or "" when it is absent or not a string.
func asString(value any) string {
	text, _ := value.(string)
	return text
}

// safeInt reads a JSON number field. JSON has one number type, so an integer
// arrives as a float; a value that is not a whole number is not an id.
func safeInt(value any) (int, bool) {
	switch number := value.(type) {
	case float64:
		if number != float64(int(number)) {
			return 0, false
		}
		return int(number), true
	case int:
		return number, true
	case json.Number:
		parsed, err := number.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	}
	return 0, false
}
