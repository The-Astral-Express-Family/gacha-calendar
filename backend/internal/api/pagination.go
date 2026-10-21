// Package api implements the JSON surface of the service.
package api

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"time"
)

// Pagination is the standard page metadata attached to every list response.
type Pagination struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
}

type listResponse struct {
	Data       any        `json:"data"`
	Pagination Pagination `json:"pagination"`
}

const (
	maxPageSize = 100
	defaultPage = 1
)

func clampPagination(r *http.Request) (page, pageSize int) {
	page = defaultPage
	pageSize = 20
	if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && v > 0 {
		page = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("page_size")); err == nil && v > 0 {
		pageSize = v
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return page, pageSize
}

func offset(page, pageSize int) int { return (page - 1) * pageSize }

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeList(w http.ResponseWriter, data any, page, pageSize int, total int64) {
	totalPages := 0
	if pageSize > 0 {
		totalPages = int(math.Ceil(float64(total) / float64(pageSize)))
	}
	writeJSON(w, http.StatusOK, listResponse{
		Data:       data,
		Pagination: Pagination{Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages},
	})
}

// parseRFC3339 validates a UTC-ish RFC3339 query parameter.
func parseRFC3339(name, value string) (time.Time, bool, string) {
	if value == "" {
		return time.Time{}, false, ""
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false, name + " 必须是 RFC3339 时间（如 2026-10-01T00:00:00Z）"
	}
	return t.UTC(), true, ""
}

// parseBoolParam returns the tristate of a boolean query parameter.
func parseBoolParam(value string) (set, val bool) {
	switch value {
	case "":
		return false, false
	case "1", "true", "yes":
		return true, true
	case "0", "false", "no":
		return true, false
	default:
		return false, false
	}
}
