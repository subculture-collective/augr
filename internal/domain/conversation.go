package domain

import (
	"time"

	"github.com/google/uuid"
)

// Conversation stores a conversation thread associated with a pipeline run.
type Conversation struct {
	ID                   uuid.UUID          `json:"id"`
	AccountID            uuid.UUID          `json:"account_id"`
	Environment          AccountEnvironment `json:"environment"`
	PipelineRunID        uuid.UUID          `json:"pipeline_run_id"`
	PipelineRunTradeDate time.Time          `json:"pipeline_run_trade_date"`
	AgentRole            AgentRole          `json:"agent_role"`
	Title                string             `json:"title,omitempty"`
	CreatedAt            time.Time          `json:"created_at"`
	UpdatedAt            time.Time          `json:"updated_at"`
}

// ConversationMessageRole identifies the sender role of a conversation message.
type ConversationMessageRole string

const (
	ConversationMessageRoleUser      ConversationMessageRole = "user"
	ConversationMessageRoleAssistant ConversationMessageRole = "assistant"
)

// String returns the string representation of a ConversationMessageRole.
func (r ConversationMessageRole) String() string {
	return string(r)
}

// ConversationMessage stores a single message in a conversation thread.
type ConversationMessage struct {
	ID             uuid.UUID               `json:"id"`
	AccountID      uuid.UUID               `json:"account_id"`
	ConversationID uuid.UUID               `json:"conversation_id"`
	Role           ConversationMessageRole `json:"role"`
	Content        string                  `json:"content"`
	CreatedAt      time.Time               `json:"created_at"`
}
