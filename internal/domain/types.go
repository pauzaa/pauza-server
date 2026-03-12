package domain

// BasicUser is the public-facing user representation used across
// handler and service boundaries.
type BasicUser struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Username          string  `json:"username"`
	ProfilePictureURL *string `json:"profile_picture_url"`
}
