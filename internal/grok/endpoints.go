// Package grok implements the reverse-proxy protocol for grok.com and
// console.x.ai: URL constants, header/cookie/statsig builders, the
// tls-client transport wrapper, chat payload builder + SSE adapter,
// console chat transport, auth (ToS/NSFW/birth-date) helpers, rate-limit
// quota fetcher, and asset upload/list/delete.
package grok

// Upstream endpoints. Mirrors app/dataplane/reverse/runtime/endpoint_table.py.
const (
	Base         = "https://grok.com"
	AssetsCDN    = "https://assets.grok.com"
	ConsoleBase  = "https://console.x.ai"
	AccountsBase = "https://accounts.x.ai"

	// App-chat (SSE streaming, new conversation).
	Chat = Base + "/rest/app-chat/conversations/new"

	// Asset management.
	AssetsUpload   = Base + "/rest/app-chat/upload-file"
	AssetsListURL  = Base + "/rest/assets"
	AssetsDeleteURL = Base + "/rest/assets-metadata" // append /{asset_id}
	AssetsDownload = AssetsCDN                       // GET /{path}

	// Rate limits (usage / quota sync).
	RateLimits = Base + "/rest/rate-limits"

	// gRPC-Web endpoints.
	AcceptTOSURL = AccountsBase + "/auth_mgmt.AuthManagement/SetTosAcceptedVersion"
	NSFWMgmtURL  = Base + "/auth_mgmt.AuthManagement/UpdateUserFeatureControls"

	// Conversation follow-up (SSE streaming, subsequent messages).
	// First message uses Chat (conversations/new); follow-ups use Responses.
	Responses     = Base + "/rest/app-chat/conversations/%s/responses"
	LoadResponses = Base + "/rest/app-chat/conversations/%s/load-responses"
	ResponseNode  = Base + "/rest/app-chat/conversations/%s/response-node"
	StopInflight  = Base + "/rest/app-chat/conversations/%s/stop-inflight-responses"
	ConversationV2 = Base + "/rest/app-chat/conversations_v2/%s"
	Conversation   = Base + "/rest/app-chat/conversations/%s"

	// Auth REST.
	SetBirthURL = Base + "/rest/auth/set-birth-date"

	// Media (video).
	MediaPost      = Base + "/rest/media/post/create"
	MediaPostLink  = Base + "/rest/media/post/create-link"
	VideoUpscale   = Base + "/rest/media/video/upscale"

	// Console API (console.x.ai).
	ConsoleResponses = ConsoleBase + "/v1/responses"
	ConsoleChat       = ConsoleBase + "/v1/chat/completions"

	// Console gRPC-Web endpoints (auth_mgmt, billing).
	ConsoleListApiKeys       = ConsoleBase + "/auth_mgmt.AuthManagement/ListApiKeys"
	ConsoleListModelsForTeam = ConsoleBase + "/auth_mgmt.AuthManagement/ListModelsForTeam"
	ConsoleGetTeam           = ConsoleBase + "/auth_mgmt.AuthManagement/GetTeam"
	ConsoleGetSpendingLimits = ConsoleBase + "/prod_mc_billing.UISvc/GetSpendingLimits"
	ConsoleListBalanceChanges = ConsoleBase + "/prod_mc_billing.UISvc/ListPrepaidBalanceChanges"
)

// DefaultUserAgent is the Chrome UA used when no cf_clearance profile is set.
const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"
