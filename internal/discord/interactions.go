package discord

// InteractionType identifies the kind of interaction Discord sent.
type InteractionType int

// ResponseType identifies the kind of response being sent back to Discord.
type ResponseType int

// Interaction type constants.
const (
	InteractionTypePing               InteractionType = 1
	InteractionTypeApplicationCommand InteractionType = 2
)

// Response type constants.
const (
	ResponseTypePong                     ResponseType = 1
	ResponseTypeChannelMessageWithSource ResponseType = 4
)

// Flag constants for interaction response data.
const FlagEphemeral = 64

// Permission bit constants for member permission checks.
const (
	PermissionAdministrator = 1 << 3 // 8
	PermissionManageGuild   = 1 << 5 // 32
)

// Interaction is the top-level payload Discord POSTs to the interactions endpoint.
type Interaction struct {
	Type      InteractionType `json:"type"`
	GuildID   string          `json:"guild_id,omitempty"`
	ChannelID string          `json:"channel_id,omitempty"`
	Channel   *ChannelData    `json:"channel,omitempty"` // partial channel object; present for guild interactions
	Data      InteractionData `json:"data"`
	Member    *MemberData     `json:"member,omitempty"` // present for guild interactions
	User      *UserData       `json:"user,omitempty"`   // present for DM interactions
}

// ChannelData is the partial channel object Discord includes on interactions
// invoked inside a guild channel.
type ChannelData struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// InteractionData carries command-specific fields.
type InteractionData struct {
	Name    string              `json:"name"`
	Options []InteractionOption `json:"options,omitempty"`
}

// InteractionOption represents a single slash-command option value.
type InteractionOption struct {
	Name string `json:"name"`
	// Value holds the option value as decoded by json.Unmarshal:
	// string for STRING options, float64 for INTEGER/NUMBER options, bool for BOOLEAN options.
	Value any `json:"value"`
}

// MemberData is the guild member who triggered the interaction.
type MemberData struct {
	User        UserData `json:"user"`
	Permissions string   `json:"permissions"` // decimal permission bitfield string sent by Discord
}

// UserData is the Discord user who triggered the interaction.
type UserData struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// InteractionResponse is the JSON body sent back to Discord.
type InteractionResponse struct {
	Type ResponseType             `json:"type"`
	Data *InteractionResponseData `json:"data,omitempty"`
}

// InteractionResponseData carries the message content and flags.
type InteractionResponseData struct {
	Content string `json:"content"`
	Flags   int    `json:"flags,omitempty"`
}

// PongResponse responds to a PING interaction.
func PongResponse() InteractionResponse {
	return InteractionResponse{Type: ResponseTypePong}
}

// MessageResponse sends a visible channel message in response to a command.
func MessageResponse(content string) InteractionResponse {
	return InteractionResponse{
		Type: ResponseTypeChannelMessageWithSource,
		Data: &InteractionResponseData{Content: content},
	}
}

// EphemeralResponse sends a message visible only to the invoking user.
func EphemeralResponse(content string) InteractionResponse {
	return InteractionResponse{
		Type: ResponseTypeChannelMessageWithSource,
		Data: &InteractionResponseData{Content: content, Flags: FlagEphemeral},
	}
}
