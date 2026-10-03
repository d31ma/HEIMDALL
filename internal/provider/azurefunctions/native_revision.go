package azurefunctions

type nativeRevision struct {
	ID         *string                   `json:"id,omitempty"`
	Name       *string                   `json:"name,omitempty"`
	Type       *string                   `json:"type,omitempty"`
	Properties *nativeRevisionProperties `json:"properties,omitempty"`
}
