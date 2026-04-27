package audit

const (
	StatusAllowed = "allowed"
	StatusDenied  = "denied"
	StatusError   = "error"
)

const (
	ActionServerList        = "server.list"
	ActionServerRegister    = "server.register"
	ActionServerDelete      = "server.delete"
	ActionServerSync        = "server.sync"
	ActionServerHealth      = "server.health"
	ActionServerDeactivated = "server.deactivated"
	ActionServerReactivated = "server.reactivated"
	ActionToolDiscover      = "tool.discover"
	ActionToolCall          = "tool.call"
	ActionAuthDeny          = "auth.deny"
)

// Event is a single audit record. ServerID==0 means N/A.
type Event struct {
	Action        string
	Status        string
	ActorSub      string
	ActorUsername string
	ActorRoles    []string
	ServerID      int64
	ToolName      string
	LatencyMS     int64
	RequestID     string
	IP            string
	UserAgent     string
	Error         string
	Metadata      map[string]any
}
