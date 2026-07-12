package domain

import "time"

type EvidenceRef struct {
	Kind string
	URI  string
	Note string
}

type ArtifactRef struct {
	ID        string
	NodeID    ProgressNodeID
	Kind      string
	URI       string
	MediaType string
	Bytes     int64
	SHA256    string
	Evidence  []EvidenceRef
	CreatedAt time.Time
}
