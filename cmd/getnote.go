package cmd

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatecannotbealtered/dedao-cli/internal/dedao"
	getnoteapi "github.com/fatecannotbealtered/dedao-cli/internal/getnote"
	"github.com/fatecannotbealtered/dedao-cli/internal/output"
	"github.com/fatecannotbealtered/dedao-cli/internal/secret"
	"github.com/spf13/cobra"
)

const (
	getnoteAPIKeySecret   = "getnote-api-key"
	getnoteClientIDSecret = "getnote-client-id"
)

func (a *application) getnoteStateDir() (string, error) {
	base, err := dedao.StateDir(a.stateDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "getnote"), nil
}

func (a *application) loadGetnoteCredentials() (string, string, string, error) {
	stateDir, err := a.getnoteStateDir()
	if err != nil {
		return "", "", "", err
	}
	store := secret.New(stateDir)
	apiKeyBytes, apiKeyErr := store.Load(getnoteAPIKeySecret)
	clientIDBytes, clientIDErr := store.Load(getnoteClientIDSecret)
	apiKey := strings.TrimSpace(string(apiKeyBytes))
	clientID := strings.TrimSpace(string(clientIDBytes))
	envAPIKey := strings.TrimSpace(os.Getenv("GETNOTE_API_KEY"))
	envClientID := strings.TrimSpace(os.Getenv("GETNOTE_CLIENT_ID"))
	if envAPIKey != "" {
		apiKeyErr = nil
		apiKey = envAPIKey
	}
	if envClientID != "" {
		clientIDErr = nil
		clientID = envClientID
	}
	if apiKeyErr != nil && !errors.Is(apiKeyErr, secret.ErrNotFound) {
		return "", "", stateDir, apiKeyErr
	}
	if clientIDErr != nil && !errors.Is(clientIDErr, secret.ErrNotFound) {
		return "", "", stateDir, clientIDErr
	}
	return apiKey, clientID, stateDir, nil
}

func getnoteCredentialSources(apiKey, clientID string) (string, string) {
	apiKeySource := "encrypted_store"
	if strings.TrimSpace(os.Getenv("GETNOTE_API_KEY")) != "" {
		apiKeySource = "environment"
	} else if apiKey == "" {
		apiKeySource = "missing"
	}
	clientIDSource := "encrypted_store"
	if strings.TrimSpace(os.Getenv("GETNOTE_CLIENT_ID")) != "" {
		clientIDSource = "environment"
	} else if clientID == "" {
		clientIDSource = "missing"
	}
	return apiKeySource, clientIDSource
}

func getnoteCredentialStorage(apiKey, clientID, stateDir string) string {
	apiKeySource, clientIDSource := getnoteCredentialSources(apiKey, clientID)
	store := secret.New(stateDir)
	backends := map[string]bool{}
	for name, source := range map[string]string{
		getnoteAPIKeySecret: apiKeySource, getnoteClientIDSecret: clientIDSource,
	} {
		switch source {
		case "environment":
			backends["environment"] = true
		case "encrypted_store":
			backend := store.StoredBackend(name)
			if backend == "" {
				backend = store.Backend()
			}
			backends[backend] = true
		}
	}
	if len(backends) == 0 {
		return "missing"
	}
	if len(backends) > 1 {
		return "mixed"
	}
	for backend := range backends {
		return backend
	}
	return "missing"
}

func loadGetnoteStoredCredential(store *secret.Store, name string) ([]byte, error) {
	value, err := store.Load(name)
	if errors.Is(err, secret.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (a *application) getnoteClient() (*getnoteapi.Client, string, error) {
	client, stateDir, _, _, err := a.getnoteClientWithCredentials()
	return client, stateDir, err
}

func (a *application) getnoteClientWithCredentials() (*getnoteapi.Client, string, string, string, error) {
	apiKey, clientID, stateDir, err := a.loadGetnoteCredentials()
	if err != nil {
		return nil, stateDir, "", "", output.WrapError("E_CONFIG", "could not read GetNote credentials", err, nil)
	}
	return getnoteapi.New(getnoteapi.Options{
		APIKey: apiKey, ClientID: clientID, BaseURL: getnoteBaseURLOverride, Timeout: a.timeout,
	}), stateDir, apiKey, clientID, nil
}

var getnoteBaseURLOverride string

func (a *application) getnoteCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "getnote", Short: "Read and manage GetNote notes", Args: cobra.NoArgs}
	cmd.AddCommand(a.getnoteAuthCommand(), a.getnoteSaveCommand(), a.getnoteTaskCommand(),
		a.getnoteNotesCommand(), a.getnoteNoteCommand(), a.getnoteSearchCommand(),
		a.getnoteTagCommand(), a.getnoteKnowledgeBasesCommand(), a.getnoteKnowledgeCommand())
	return cmd
}

func (a *application) getnoteAuthCommand() *cobra.Command {
	auth := &cobra.Command{Use: "auth", Short: "Manage GetNote API credentials", Args: cobra.NoArgs}
	var apiKey, clientID string
	var apiKeyStdin bool
	login := &cobra.Command{
		Use: "login", Short: "Store GetNote API credentials in the encrypted local store", Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if apiKeyStdin && strings.TrimSpace(apiKey) != "" {
				return output.NewError("E_USAGE", "--api-key and --api-key-stdin cannot be used together", nil)
			}
			if apiKeyStdin {
				var err error
				var raw []byte
				raw, err = io.ReadAll(a.in)
				if err != nil {
					return output.WrapError("E_IO", "could not read the API key from stdin", err, nil)
				}
				apiKey = strings.TrimSpace(string(raw))
			}
			if strings.TrimSpace(apiKey) == "" {
				apiKey = strings.TrimSpace(os.Getenv("GETNOTE_API_KEY"))
			}
			if strings.TrimSpace(clientID) == "" {
				clientID = strings.TrimSpace(os.Getenv("GETNOTE_CLIENT_ID"))
			}
			if apiKey == "" || clientID == "" {
				return output.NewError("E_VALIDATION", "--api-key and --client-id are required (or set GETNOTE_API_KEY and GETNOTE_CLIENT_ID)", nil)
			}
			stateDir, err := a.getnoteStateDir()
			if err != nil {
				return output.WrapError("E_CONFIG", "could not resolve GetNote state directory", err, nil)
			}
			store := secret.New(stateDir)
			if err := store.Save(getnoteAPIKeySecret, []byte(strings.TrimSpace(apiKey))); err != nil {
				return output.WrapError("E_IO", "could not store GetNote API key", err, nil)
			}
			if err := store.Save(getnoteClientIDSecret, []byte(strings.TrimSpace(clientID))); err != nil {
				_ = store.Delete(getnoteAPIKeySecret)
				return output.WrapError("E_IO", "could not store GetNote client ID", err, nil)
			}
			return a.success(map[string]any{"configured": true, "stored": true})
		},
	}
	login.Flags().StringVar(&apiKey, "api-key", "", "GetNote API key (prefer --api-key-stdin or GETNOTE_API_KEY)")
	login.Flags().BoolVar(&apiKeyStdin, "api-key-stdin", false, "Read the API key from stdin")
	login.Flags().StringVar(&clientID, "client-id", "", "GetNote client ID (or GETNOTE_CLIENT_ID)")
	auth.AddCommand(login)
	auth.AddCommand(&cobra.Command{Use: "status", Short: "Report GetNote credential configuration", Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			apiKey, clientID, stateDir, err := a.loadGetnoteCredentials()
			if err != nil {
				return output.WrapError("E_CONFIG", "could not read GetNote credentials", err, nil)
			}
			apiKeySource, clientIDSource := getnoteCredentialSources(apiKey, clientID)
			return a.success(map[string]any{
				"configured": apiKey != "" && clientID != "", "api_key_configured": apiKey != "",
				"client_id_configured": clientID != "", "api_key_source": apiKeySource,
				"client_id_source": clientIDSource, "state_dir": stateDir,
			})
		},
	})
	var dryRun bool
	var confirm string
	logout := &cobra.Command{Use: "logout", Short: "Remove GetNote credentials from the encrypted local store", Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if dryRun && confirm != "" {
				return output.NewError("E_USAGE", "--dry-run and --confirm cannot be used together", nil)
			}
			stateDir, err := a.getnoteStateDir()
			if err != nil {
				return output.WrapError("E_CONFIG", "could not resolve GetNote state directory", err, nil)
			}
			store := secret.New(stateDir)
			apiKey, err := loadGetnoteStoredCredential(store, getnoteAPIKeySecret)
			if err != nil {
				return output.WrapError("E_CONFIG", "could not read the stored GetNote API key", err, nil)
			}
			clientID, err := loadGetnoteStoredCredential(store, getnoteClientIDSecret)
			if err != nil {
				return output.WrapError("E_CONFIG", "could not read the stored GetNote client ID", err, nil)
			}
			stored := len(apiKey) > 0 || len(clientID) > 0
			environmentActive := strings.TrimSpace(os.Getenv("GETNOTE_API_KEY")) != "" && strings.TrimSpace(os.Getenv("GETNOTE_CLIENT_ID")) != ""
			payload := map[string]any{
				"stored":                 stored,
				"credential_fingerprint": getnoteCredentialFingerprint(string(apiKey), string(clientID)),
			}
			if dryRun {
				token, expires, err := getnoteConfirmToken(stateDir, "dedao-cli getnote auth logout", payload, payload)
				if err != nil {
					return output.WrapError("E_IO", "could not create a confirmation token", err, nil)
				}
				return a.success(map[string]any{
					"preview":                        map[string]any{"changes": []map[string]any{{"action": "delete", "resource": "getnote_stored_credentials", "before": map[string]any{"configured": stored}, "after": nil}}},
					"environment_credentials_active": environmentActive, "confirm_token": token, "expires_at": expires.Format(time.RFC3339),
				})
			}
			if err := validateGetnoteConfirmToken(confirm, stateDir, "dedao-cli getnote auth logout", payload, payload); err != nil {
				return err
			}
			if err := store.Delete(getnoteAPIKeySecret); err != nil {
				return output.WrapError("E_IO", "could not remove GetNote API key", err, nil)
			}
			if err := store.Delete(getnoteClientIDSecret); err != nil {
				return output.WrapError("E_IO", "could not remove GetNote client ID", err, nil)
			}
			return a.success(map[string]any{
				"stored_credentials_removed":     true,
				"environment_credentials_active": environmentActive,
				"configured":                     environmentActive,
			})
		},
	}
	logout.Flags().BoolVar(&dryRun, "dry-run", false, "Preview credential deletion and issue a confirmation token")
	logout.Flags().StringVar(&confirm, "confirm", "", "Execute credential deletion with a token returned by --dry-run")
	auth.AddCommand(logout)
	return auth
}

func getnoteCredentialFingerprint(apiKey, clientID string) string {
	digest := sha256.Sum256([]byte(apiKey + "\x00" + clientID))
	return fmt.Sprintf("%x", digest[:])
}

func getnoteContextFingerprint(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", digest[:])
}

func getnoteWriteGuard(apiKey, clientID string, body map[string]any) map[string]any {
	// GetNote exposes no permission-introspection endpoint. The API key binds
	// the effective account while the client ID binds the configured app/scope
	// context; rotating either credential invalidates an old confirmation.
	return map[string]any{
		"payload":            body,
		"account_context":    getnoteContextFingerprint(apiKey),
		"permission_context": getnoteContextFingerprint(clientID),
	}
}

func getnoteWriteScope(ctx context.Context, client *getnoteapi.Client, guard map[string]any, body map[string]any) (map[string]any, []map[string]any, error) {
	scope := make(map[string]any, len(guard)+1)
	for key, value := range guard {
		scope[key] = value
	}
	targetVersions := make([]map[string]any, 0)
	for _, noteID := range getnoteTargetNoteIDs(body) {
		detail, err := client.Note(ctx, noteID)
		if err != nil {
			return nil, nil, err
		}
		targetVersions = append(targetVersions, getnoteTargetVersion(detail, noteID))
	}
	if len(targetVersions) > 0 {
		scope["target_versions"] = targetVersions
	}
	return scope, targetVersions, nil
}

func getnoteTargetNoteIDs(body map[string]any) []string {
	ids := make([]string, 0)
	seen := map[string]bool{}
	appendID := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			ids = append(ids, value)
		}
	}
	for _, key := range []string{"note_id", "id"} {
		if value, ok := body[key].(string); ok {
			appendID(value)
		}
	}
	if values, ok := body["note_ids"].([]string); ok {
		for _, value := range values {
			appendID(value)
		}
	}
	return ids
}

func getnoteTargetVersion(value any, fallbackID string) map[string]any {
	result := map[string]any{"note_id": fallbackID}
	object, _ := value.(map[string]any)
	note, _ := object["note"].(map[string]any)
	if id, exists := note["note_id"]; exists {
		result["note_id"] = id
	} else if id, exists := note["id"]; exists {
		result["note_id"] = id
	}
	if version, exists := note["version"]; exists {
		result["version"] = version
	}
	if updatedAt, exists := note["updated_at"]; exists {
		result["updated_at"] = updatedAt
	}
	return result
}

func (a *application) getnoteSaveCommand() *cobra.Command {
	var noteType, content, linkURL, title, topicID, parentID, requestID string
	var imageURLs, tags []string
	var dryRun, wait bool
	var confirm string
	var pollInterval, pollTimeout time.Duration
	cmd := &cobra.Command{Use: "save", Short: "Save a text, link, or image note", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dryRun && confirm != "" {
				return output.NewError("E_USAGE", "--dry-run and --confirm cannot be used together", nil)
			}
			if noteType == "" {
				switch {
				case linkURL != "":
					noteType = "link"
				case len(imageURLs) > 0:
					noteType = "img_text"
				default:
					noteType = "plain_text"
				}
			}
			if noteType != "plain_text" && noteType != "link" && noteType != "img_text" {
				return output.NewError("E_VALIDATION", "--note-type must be plain_text, link, or img_text", nil)
			}
			if noteType == "plain_text" && strings.TrimSpace(content) == "" || noteType == "link" && strings.TrimSpace(linkURL) == "" || noteType == "img_text" && len(imageURLs) == 0 {
				return output.NewError("E_VALIDATION", "content for the selected note type is required", nil)
			}
			if noteType != "plain_text" && content != "" || noteType != "link" && linkURL != "" || noteType != "img_text" && len(imageURLs) > 0 {
				return output.NewError("E_VALIDATION", "content flags must match --note-type", nil)
			}
			if !dryRun && confirm == "" {
				return output.NewError("E_CONFIRMATION_REQUIRED", "confirmation required: run this command with --dry-run, then retry with --confirm <confirm_token>", nil)
			}
			body := map[string]any{"note_type": noteType}
			if content != "" {
				body["content"] = content
			}
			if linkURL != "" {
				body["link_url"] = linkURL
			}
			if len(imageURLs) > 0 {
				body["image_urls"] = imageURLs
			}
			if title != "" {
				body["title"] = title
			}
			if len(tags) > 0 {
				body["tags"] = tags
			}
			if topicID != "" {
				body["topic_id"] = topicID
			}
			if parentID != "" {
				body["parent_id"] = parentID
			}
			if requestID != "" {
				body["client_request_id"] = requestID
			}
			stateDir, err := a.getnoteStateDir()
			if err != nil {
				return err
			}
			client, _, apiKey, clientID, err := a.getnoteClientWithCredentials()
			if err != nil {
				return err
			}
			guard := getnoteWriteGuard(apiKey, clientID, body)
			if dryRun {
				if !client.Configured() {
					return getnoteapi.ErrAuthRequired
				}
				scope, _, err := getnoteWriteScope(cmd.Context(), client, guard, body)
				if err != nil {
					return err
				}
				token, expires, err := getnoteConfirmToken(stateDir, "dedao-cli getnote save", scope, guard)
				if err != nil {
					return output.WrapError("E_IO", "could not create a confirmation token", err, nil)
				}
				return a.success(map[string]any{"preview": map[string]any{"changes": []map[string]any{{"action": "create", "resource": "note", "after": body}}}, "confirm_token": token, "expires_at": expires.Format(time.RFC3339)})
			}
			if err := validateGetnoteConfirmGuard(confirm, stateDir, "dedao-cli getnote save", guard); err != nil {
				return err
			}
			if !client.Configured() {
				return getnoteapi.ErrAuthRequired
			}
			scope, _, err := getnoteWriteScope(cmd.Context(), client, guard, body)
			if err != nil {
				return err
			}
			if err := validateGetnoteConfirmToken(confirm, stateDir, "dedao-cli getnote save", scope, guard); err != nil {
				return err
			}
			result, err := client.Save(cmd.Context(), body)
			if err != nil {
				return err
			}
			if wait {
				return a.waitGetnoteTask(cmd.Context(), client, result, pollInterval, pollTimeout)
			}
			return a.success(map[string]any{"result": result})
		},
	}
	cmd.Flags().StringVar(&noteType, "note-type", "", "Note type: plain_text, link, or img_text")
	cmd.Flags().StringVar(&content, "content", "", "Text note content")
	cmd.Flags().StringVar(&linkURL, "link-url", "", "URL for a link note")
	cmd.Flags().StringSliceVar(&imageURLs, "image-url", nil, "Image URL for an image note (repeatable)")
	cmd.Flags().StringVar(&title, "title", "", "Optional note title")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "Optional tag (repeatable)")
	cmd.Flags().StringVar(&topicID, "topic-id", "", "Optional knowledge base topic ID")
	cmd.Flags().StringVar(&parentID, "parent-id", "", "Optional parent note ID")
	cmd.Flags().StringVar(&requestID, "idempotency-key", "", "Stable key for safely retrying note creation")
	cmd.Flags().BoolVar(&wait, "wait", false, "Poll an asynchronous save task until it completes")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", 2*time.Second, "Interval for --wait")
	cmd.Flags().DurationVar(&pollTimeout, "poll-timeout", 60*time.Second, "Maximum time for --wait")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview note creation and issue a confirmation token")
	cmd.Flags().StringVar(&confirm, "confirm", "", "Execute note creation with a token returned by --dry-run")
	return cmd
}

func (a *application) waitGetnoteTask(ctx context.Context, client *getnoteapi.Client, result any, interval, timeout time.Duration) error {
	taskID := findString(result, "task_id")
	if taskID == "" {
		return a.success(map[string]any{"result": result})
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		progress, err := client.Task(ctx, taskID)
		if err != nil {
			return err
		}
		status := strings.ToLower(findString(progress, "status"))
		if status == "success" || status == "done" || status == "completed" {
			return a.success(map[string]any{"result": progress})
		}
		if status == "failed" || status == "error" {
			return output.NewError("E_SERVER", "GetNote save task failed", map[string]any{
				"task_id": taskID, "progress": progress, "_untrusted": []string{"progress"},
			})
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func findString(value any, key string) string {
	switch current := value.(type) {
	case map[string]any:
		object := current
		if found, ok := object[key]; ok {
			if s, ok := found.(string); ok {
				return s
			}
		}
		for _, child := range object {
			if found := findString(child, key); found != "" {
				return found
			}
		}
	case []any:
		for _, child := range current {
			if found := findString(child, key); found != "" {
				return found
			}
		}
	}
	return ""
}

func getnoteList(value any, field string, page, limit int, cursorMode bool) map[string]any {
	result := map[string]any{"items": []any{}, "count": 0, "has_more": false}
	if cursorMode {
		result["next_cursor"] = nil
	}
	object, _ := value.(map[string]any)
	localCursor := ""
	if items, ok := object[field].([]any); ok {
		truncated := limit > 0 && len(items) > limit
		if truncated {
			items = items[:limit]
			result["truncated"] = true
		}
		result["items"] = items
		result["count"] = len(items)
		if truncated {
			result["has_more"] = true
			if cursorMode {
				localCursor = getnoteItemID(items[len(items)-1])
			}
		}
	}
	if hasMore, ok := object["has_more"].(bool); ok && result["has_more"] != true {
		result["has_more"] = hasMore
	}
	if total, exists := object["total"]; exists {
		result["total"] = total
	}
	if hasMore, _ := result["has_more"].(bool); hasMore && cursorMode {
		if localCursor != "" {
			result["next_cursor"] = localCursor
		} else {
			for _, key := range []string{"next_cursor", "cursor"} {
				cursor, exists := object[key]
				if !exists {
					continue
				}
				text := strings.TrimSpace(fmt.Sprint(cursor))
				if text != "" && text != "0" && text != "<nil>" {
					result["next_cursor"] = text
					break
				}
			}
		}
	}
	if page > 0 {
		result["page"] = page
		if hasMore, _ := result["has_more"].(bool); hasMore {
			result["next_page"] = page + 1
		}
	}
	return result
}

func getnoteItemID(value any) string {
	object, _ := value.(map[string]any)
	for _, key := range []string{"note_id", "id"} {
		if id, exists := object[key]; exists {
			text := strings.TrimSpace(fmt.Sprint(id))
			if text != "" && text != "0" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func getnoteTags(value any) map[string]any {
	result := map[string]any{"tags": []any{}}
	object, _ := value.(map[string]any)
	note, _ := object["note"].(map[string]any)
	if id, exists := note["note_id"]; exists {
		result["note_id"] = id
	} else if id, exists := note["id"]; exists {
		result["note_id"] = id
	}
	if tags, exists := note["tags"]; exists {
		result["tags"] = tags
	}
	return result
}

func (a *application) getnoteTaskCommand() *cobra.Command {
	return &cobra.Command{Use: "task <task-id>", Short: "Check an asynchronous GetNote task", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := a.getnoteClient()
		if err != nil {
			return err
		}
		result, err := c.Task(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return a.success(result)
	}}
}

func (a *application) getnoteNotesCommand() *cobra.Command {
	var cursor string
	var pageSize int
	cmd := &cobra.Command{Use: "notes", Short: "List GetNote notes", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if pageSize <= 0 {
			return output.NewError("E_VALIDATION", "--page-size must be greater than zero", nil)
		}
		c, _, err := a.getnoteClient()
		if err != nil {
			return err
		}
		q := urlValues()
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		if pageSize > 0 {
			q.Set("limit", fmt.Sprint(pageSize))
		}
		result, err := c.Notes(cmd.Context(), q)
		if err != nil {
			return err
		}
		return a.success(getnoteList(result, "notes", 0, pageSize, true))
	}}
	cmd.Flags().StringVar(&cursor, "cursor", "", "Cursor returned by the previous page")
	cmd.Flags().IntVar(&pageSize, "page-size", 20, "Items to return")
	return cmd
}

func urlValues() url.Values { return url.Values{} }

func (a *application) getnoteNoteCommand() *cobra.Command {
	note := &cobra.Command{Use: "note", Short: "Read and manage GetNote notes", Args: cobra.NoArgs}
	get := &cobra.Command{Use: "get <note-id>", Short: "Get a GetNote note", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := a.getnoteClient()
		if err != nil {
			return err
		}
		result, err := c.Note(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return a.success(result)
	}}
	var title, content string
	var tags []string
	var dryRun, shareExcludeAudio bool
	var confirm string
	update := &cobra.Command{Use: "update <note-id>", Short: "Update a GetNote note", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]any{"id": args[0]}
		if title != "" {
			body["title"] = title
		}
		if content != "" {
			body["content"] = content
		}
		if len(tags) > 0 {
			body["tags"] = tags
		}
		if len(body) == 1 {
			return output.NewError("E_VALIDATION", "at least one of --title, --content, or --tag is required", nil)
		}
		return a.getnoteWrite(cmd, "dedao-cli getnote note update", body, dryRun, confirm, func(ctx context.Context, c *getnoteapi.Client) (any, error) { return c.Update(ctx, body) })
	}}
	update.Flags().StringVar(&title, "title", "", "Replacement note title")
	update.Flags().StringVar(&content, "content", "", "Replacement note content")
	update.Flags().StringSliceVar(&tags, "tag", nil, "Replacement tags (repeatable)")
	update.Flags().BoolVar(&dryRun, "dry-run", false, "Preview note update and issue a confirmation token")
	update.Flags().StringVar(&confirm, "confirm", "", "Execute note update with a token returned by --dry-run")
	var deleteDry bool
	var deleteConfirm string
	deleteCmd := &cobra.Command{Use: "delete <note-id>", Short: "Delete a GetNote note", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]any{"note_id": args[0]}
		return a.getnoteWrite(cmd, "dedao-cli getnote note delete", body, deleteDry, deleteConfirm, func(ctx context.Context, c *getnoteapi.Client) (any, error) { return c.Delete(ctx, args[0]) })
	}}
	deleteCmd.Flags().BoolVar(&deleteDry, "dry-run", false, "Preview note deletion and issue a confirmation token")
	deleteCmd.Flags().StringVar(&deleteConfirm, "confirm", "", "Execute note deletion with a token returned by --dry-run")
	var shareDry bool
	var shareConfirm string
	share := &cobra.Command{Use: "share <note-id>", Short: "Create a public GetNote share link", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]any{"note_id": args[0], "share_exclude_audio": shareExcludeAudio}
		return a.getnoteWrite(cmd, "dedao-cli getnote note share", body, shareDry, shareConfirm, func(ctx context.Context, c *getnoteapi.Client) (any, error) {
			return c.Share(ctx, args[0], shareExcludeAudio)
		})
	}}
	share.Flags().BoolVar(&shareDry, "dry-run", false, "Preview public sharing and issue a confirmation token")
	share.Flags().StringVar(&shareConfirm, "confirm", "", "Execute sharing with a token returned by --dry-run")
	share.Flags().BoolVar(&shareExcludeAudio, "exclude-audio", false, "Exclude audio from the public share")
	note.AddCommand(get, update, deleteCmd, share)
	return note
}

func (a *application) getnoteWrite(cmd *cobra.Command, command string, body map[string]any, dryRun bool, confirm string, call func(context.Context, *getnoteapi.Client) (any, error)) error {
	if dryRun && confirm != "" {
		return output.NewError("E_USAGE", "--dry-run and --confirm cannot be used together", nil)
	}
	stateDir, err := a.getnoteStateDir()
	if err != nil {
		return output.WrapError("E_CONFIG", "could not resolve GetNote state directory", err, nil)
	}
	if !dryRun && confirm == "" {
		return output.NewError("E_CONFIRMATION_REQUIRED", "confirmation required: run this command with --dry-run, then retry with --confirm <confirm_token>", nil)
	}
	c, _, apiKey, clientID, err := a.getnoteClientWithCredentials()
	if err != nil {
		return err
	}
	guard := getnoteWriteGuard(apiKey, clientID, body)
	if dryRun {
		if !c.Configured() {
			return getnoteapi.ErrAuthRequired
		}
		scope, targetVersions, err := getnoteWriteScope(cmd.Context(), c, guard, body)
		if err != nil {
			return err
		}
		token, expires, err := getnoteConfirmToken(stateDir, command, scope, guard)
		if err != nil {
			return output.WrapError("E_IO", "could not create a confirmation token", err, nil)
		}
		action := getnoteWriteAction(command)
		change := map[string]any{"action": action, "resource": strings.TrimPrefix(command, "dedao-cli getnote ")}
		if len(targetVersions) > 0 {
			change["before"] = map[string]any{"target_versions": targetVersions}
		}
		if action == "delete" {
			change["target"] = body
			change["after"] = nil
		} else {
			change["after"] = body
		}
		return a.success(map[string]any{"preview": map[string]any{"changes": []map[string]any{change}}, "confirm_token": token, "expires_at": expires.Format(time.RFC3339)})
	}
	if err := validateGetnoteConfirmGuard(confirm, stateDir, command, guard); err != nil {
		return err
	}
	if !c.Configured() {
		return getnoteapi.ErrAuthRequired
	}
	scope, _, err := getnoteWriteScope(cmd.Context(), c, guard, body)
	if err != nil {
		var apiErr *getnoteapi.APIError
		if errors.As(err, &apiErr) {
			businessCode, known := knownGetnoteBusinessCode(apiErr.BusinessCode)
			if apiErr.StatusCode == http.StatusNotFound || known && businessCode == "E_NOT_FOUND" {
				return output.NewError("E_CONFLICT", "the target changed or disappeared after the preview; re-run with --dry-run", nil)
			}
		}
		return err
	}
	if err := validateGetnoteConfirmToken(confirm, stateDir, command, scope, guard); err != nil {
		return err
	}
	result, err := call(cmd.Context(), c)
	if err != nil {
		return err
	}
	return a.success(map[string]any{"result": result})
}

func getnoteWriteAction(command string) string {
	switch {
	case strings.HasSuffix(command, " delete"), strings.HasSuffix(command, " remove"):
		return "delete"
	case strings.HasSuffix(command, " share"):
		return "publish"
	case strings.HasSuffix(command, " create"), strings.HasSuffix(command, " save"):
		return "create"
	default:
		return "update"
	}
}

func (a *application) getnoteSearchCommand() *cobra.Command {
	var topicID string
	var topK int
	cmd := &cobra.Command{Use: "search <query>", Short: "Search GetNote notes", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if topK <= 0 || topK > 10 {
			return output.NewError("E_VALIDATION", "--top-k must be between 1 and 10", nil)
		}
		c, _, err := a.getnoteClient()
		if err != nil {
			return err
		}
		result, err := c.Search(cmd.Context(), args[0], topicID, topK)
		if err != nil {
			return err
		}
		return a.success(getnoteList(result, "results", 0, topK, false))
	}}
	cmd.Flags().StringVar(&topicID, "topic-id", "", "Limit search to one knowledge base topic ID")
	cmd.Flags().IntVar(&topK, "top-k", 10, "Maximum semantic matches")
	return cmd
}

func (a *application) getnoteTagCommand() *cobra.Command {
	tag := &cobra.Command{Use: "tag", Short: "Manage GetNote note tags", Args: cobra.NoArgs}
	var tags []string
	var dryRun bool
	var confirm string
	add := &cobra.Command{Use: "add <note-id>", Short: "Add tags to a note", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]any{"note_id": args[0], "tags": tags}
		if len(tags) == 0 {
			return output.NewError("E_VALIDATION", "at least one --tag is required", nil)
		}
		return a.getnoteWrite(cmd, "dedao-cli getnote tag add", body, dryRun, confirm, func(ctx context.Context, c *getnoteapi.Client) (any, error) { return c.Tags(ctx, "add", body) })
	}}
	add.Flags().StringSliceVar(&tags, "tag", nil, "Tag name (repeatable)")
	add.Flags().BoolVar(&dryRun, "dry-run", false, "Preview tag addition and issue a confirmation token")
	add.Flags().StringVar(&confirm, "confirm", "", "Execute tag addition with a token returned by --dry-run")
	var removeDry bool
	var removeConfirm string
	remove := &cobra.Command{Use: "remove <note-id> <tag-id>", Short: "Remove a tag from a note", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]any{"note_id": args[0], "tag_id": args[1]}
		return a.getnoteWrite(cmd, "dedao-cli getnote tag remove", body, removeDry, removeConfirm, func(ctx context.Context, c *getnoteapi.Client) (any, error) { return c.Tags(ctx, "delete", body) })
	}}
	remove.Flags().BoolVar(&removeDry, "dry-run", false, "Preview tag removal and issue a confirmation token")
	remove.Flags().StringVar(&removeConfirm, "confirm", "", "Execute tag removal with a token returned by --dry-run")
	list := &cobra.Command{Use: "list <note-id>", Short: "List tags attached to a note", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := a.getnoteClient()
		if err != nil {
			return err
		}
		result, err := c.TagList(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return a.success(getnoteTags(result))
	}}
	tag.AddCommand(add, remove, list)
	return tag
}

func (a *application) getnoteKnowledgeBasesCommand() *cobra.Command {
	var page int
	var pageSize int
	cmd := &cobra.Command{Use: "kbs", Short: "List GetNote knowledge bases", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if page <= 0 || pageSize <= 0 {
			return output.NewError("E_VALIDATION", "--page and --page-size must be greater than zero", nil)
		}
		c, _, err := a.getnoteClient()
		if err != nil {
			return err
		}
		q := urlValues()
		q.Set("page", fmt.Sprint(page))
		q.Set("limit", fmt.Sprint(pageSize))
		result, err := c.KnowledgeBases(cmd.Context(), q)
		if err != nil {
			return err
		}
		return a.success(getnoteList(result, "topics", page, pageSize, false))
	}}
	cmd.Flags().IntVar(&page, "page", 1, "Page number")
	cmd.Flags().IntVar(&pageSize, "page-size", 20, "Items to return")
	return cmd
}

func (a *application) getnoteKnowledgeCommand() *cobra.Command {
	kb := &cobra.Command{Use: "kb", Short: "Manage GetNote knowledge bases", Args: cobra.NoArgs}
	var page int
	var pageSize int
	var name, description string
	var dryRun bool
	var confirm string
	notes := &cobra.Command{Use: "notes <topic-id>", Short: "List notes in a knowledge base", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if page <= 0 || pageSize <= 0 {
			return output.NewError("E_VALIDATION", "--page and --page-size must be greater than zero", nil)
		}
		c, _, err := a.getnoteClient()
		if err != nil {
			return err
		}
		q := urlValues()
		q.Set("topic_id", args[0])
		q.Set("page", fmt.Sprint(page))
		q.Set("limit", fmt.Sprint(pageSize))
		result, err := c.KnowledgeNotes(cmd.Context(), args[0], q)
		if err != nil {
			return err
		}
		return a.success(getnoteList(result, "notes", page, pageSize, false))
	}}
	notes.Flags().IntVar(&page, "page", 1, "Page number")
	notes.Flags().IntVar(&pageSize, "page-size", 20, "Items to return")
	create := &cobra.Command{Use: "create", Short: "Create a GetNote knowledge base", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(name) == "" {
			return output.NewError("E_VALIDATION", "--name is required", nil)
		}
		body := map[string]any{"name": name}
		if description != "" {
			body["description"] = description
		}
		return a.getnoteWrite(cmd, "dedao-cli getnote kb create", body, dryRun, confirm, func(ctx context.Context, c *getnoteapi.Client) (any, error) { return c.KnowledgeCreate(ctx, body) })
	}}
	create.Flags().StringVar(&name, "name", "", "Knowledge base name")
	create.Flags().StringVar(&description, "description", "", "Knowledge base description")
	create.Flags().BoolVar(&dryRun, "dry-run", false, "Preview knowledge base creation and issue a confirmation token")
	create.Flags().StringVar(&confirm, "confirm", "", "Execute knowledge base creation with a token returned by --dry-run")
	var topicID string
	var noteID string
	var addDry, removeDry bool
	var addConfirm, removeConfirm string
	add := &cobra.Command{Use: "add", Short: "Add a note to a knowledge base", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		topicID = strings.TrimSpace(topicID)
		noteID = strings.TrimSpace(noteID)
		if topicID == "" || noteID == "" {
			return output.NewError("E_VALIDATION", "--topic-id and --note-id are required", nil)
		}
		body := map[string]any{"topic_id": topicID, "note_ids": []string{noteID}}
		return a.getnoteWrite(cmd, "dedao-cli getnote kb add", body, addDry, addConfirm, func(ctx context.Context, c *getnoteapi.Client) (any, error) { return c.KnowledgeAdd(ctx, body) })
	}}
	add.Flags().StringVar(&topicID, "topic-id", "", "Knowledge base topic ID")
	add.Flags().StringVar(&noteID, "note-id", "", "Note ID")
	add.Flags().BoolVar(&addDry, "dry-run", false, "Preview knowledge base membership change and issue a confirmation token")
	add.Flags().StringVar(&addConfirm, "confirm", "", "Execute membership change with a token returned by --dry-run")
	remove := &cobra.Command{Use: "remove", Short: "Remove a note from a knowledge base", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		topicID = strings.TrimSpace(topicID)
		noteID = strings.TrimSpace(noteID)
		if topicID == "" || noteID == "" {
			return output.NewError("E_VALIDATION", "--topic-id and --note-id are required", nil)
		}
		body := map[string]any{"topic_id": topicID, "note_ids": []string{noteID}}
		return a.getnoteWrite(cmd, "dedao-cli getnote kb remove", body, removeDry, removeConfirm, func(ctx context.Context, c *getnoteapi.Client) (any, error) { return c.KnowledgeRemove(ctx, body) })
	}}
	remove.Flags().StringVar(&topicID, "topic-id", "", "Knowledge base topic ID")
	remove.Flags().StringVar(&noteID, "note-id", "", "Note ID")
	remove.Flags().BoolVar(&removeDry, "dry-run", false, "Preview knowledge base membership removal and issue a confirmation token")
	remove.Flags().StringVar(&removeConfirm, "confirm", "", "Execute membership removal with a token returned by --dry-run")
	kb.AddCommand(notes, create, add, remove)
	return kb
}
