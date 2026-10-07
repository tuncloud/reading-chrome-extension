package dynamo

// Sort keys and key builders for every item in spec §3.1.
const (
	SKProfile  = "PROFILE"
	SKNUser    = "USER"
	SKDevToken = "DEVTOKEN"
	SKMeta     = "META"
	SKState    = "STATE"
	SKCode     = "CODE"
)

func UserPK(uid string) string           { return "USER#" + uid }
func NUserPK(notionUID string) string    { return "NUSER#" + notionUID }
func DeviceSK(deviceID string) string    { return "DEVICE#" + deviceID }
func DevTokenPK(tokenHMAC string) string { return "DEVTOKEN#" + tokenHMAC }

// InstPK is both the installation partition key and, under USER#<uid>, the pointer sort key.
func InstPK(botID string) string        { return "INST#" + botID }
func ArticleSK(urlHash string) string   { return "ART#" + urlHash }
func SegmentSK(segmentID string) string { return "SEG#" + segmentID }
func DaySK(date string) string          { return "DAY#" + date }
func OAuthStatePK(state string) string  { return "OAUTHSTATE#" + state }
func AuthCodePK(code string) string     { return "AUTHCODE#" + code }
