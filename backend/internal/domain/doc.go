// Package domain holds the reading tracker's entities and business rules. It imports
// only the standard library: no storage tags and no Notion, AWS or HTTP types.
// Timestamps are RFC 3339 UTC strings with second precision (see Timestamp), which keeps
// them comparable as strings in storage conditions.
package domain
