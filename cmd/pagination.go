package cmd

import (
	"strconv"

	"github.com/fatecannotbealtered/dedao-cli/internal/output"
	"github.com/spf13/cobra"
)

func (a *application) applyLimit(cmd *cobra.Command) error {
	limitFlag := cmd.Flags().Lookup("limit")
	if limitFlag == nil || !limitFlag.Changed {
		return nil
	}
	if a.limit <= 0 {
		return output.NewError("E_VALIDATION", "--limit must be greater than zero", nil)
	}
	for _, name := range []string{"page-size", "count", "top-k"} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			continue
		}
		if flag.Changed {
			return output.NewError("E_VALIDATION", "--limit cannot be combined with --"+name, nil)
		}
		if err := cmd.Flags().Set(name, strconv.Itoa(a.limit)); err != nil {
			return output.WrapError("E_VALIDATION", "could not apply --limit", err, nil)
		}
		return nil
	}
	return output.NewError("E_VALIDATION", cmd.CommandPath()+" does not support --limit", nil)
}

func normalizePagination(data any, limit int) any {
	object, ok := data.(map[string]any)
	if !ok {
		return data
	}

	for _, key := range []string{"items", "list", "article_list", "product_list", "moduleList", "hot_tab_list"} {
		items, ok := object[key].([]any)
		if !ok {
			continue
		}

		hasMore, known := paginationHasMore(object)
		if limit > 0 && len(items) > limit {
			items = items[:limit]
			object[key] = items
			hasMore, known = true, true
		} else if limit > 0 && len(items) < limit && !known {
			hasMore, known = false, true
		}
		object["count"] = len(items)
		if known {
			object["has_more"] = hasMore
		}
		return object
	}
	return data
}

func paginationHasMore(object map[string]any) (bool, bool) {
	for _, key := range []string{"has_more", "is_more", "isMore"} {
		if value, ok := object[key].(bool); ok {
			return value, true
		}
	}
	return false, false
}
