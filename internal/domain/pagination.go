package domain

// PaginationResult holds page-based pagination metadata.
type PaginationResult struct {
	Page  int `json:"page"`
	Limit int `json:"limit"`
	Total int `json:"total"`
}
