package discord

// Interaction type constants.
const (
	InteractionTypePing               = 1
	InteractionTypeApplicationCommand = 2
)

// Response type constants.
const (
	ResponseTypePong                     = 1
	ResponseTypeChannelMessageWithSource = 4
)

// Flag constants for interaction response data.
const FlagEphemeral = 64

// Interaction is the top-level payload Discord POSTs to the interactions endpoint.
type Interaction struct {
	Type   int             `json:"type"`
	Data   InteractionData `json:"data"`
	Member *MemberData     `json:"member,omitempty"` // present for guild interactions
	User   *UserData       `json:"user,omitempty"`   // present for DM interactions
}

// InteractionData carries command-specific fields.
type InteractionData struct {
	Name    string              `json:"name"`
	Options []InteractionOption `json:"options,omitempty"`
}

// InteractionOption represents a single slash-command option value.
type InteractionOption struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// MemberData is the guild member who triggered the interaction.
type MemberData struct {
	User UserData `json:"user"`
}

// UserData is the Discord user who triggered the interaction.
type UserData struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// InteractionResponse is the JSON body sent back to Discord.
type InteractionResponse struct {
	Type int                      `json:"type"`
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
