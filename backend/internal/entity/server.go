package entity

import "time"

type Server struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Endpoint    string    `json:"endpoint"`
	Description string    `json:"description"`
	Owner       string    `json:"owner"`
	AuthType    string    `json:"authType"`
	Tags        []string  `json:"tags"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"createdAt"`

	KeycloakClientID    string     `json:"keycloakClientId,omitempty"`
	KeycloakInternalID  string     `json:"-"`
	TLSCertSHA256       string     `json:"tlsCertSha256,omitempty"`
	TLSCertCapturedAt   *time.Time `json:"tlsCertCapturedAt,omitempty"`
}
