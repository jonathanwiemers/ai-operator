package v1alpha1

import "unicode/utf8"

// AppendOwnerMessage adds m to list unless a message with its ID is there,
// cuts its text to MaxOwnerMessageText characters and keeps the last
// MaxOwnerMessages.
func AppendOwnerMessage(list []OwnerMessage, m OwnerMessage) []OwnerMessage {
	for _, e := range list {
		if e.ID == m.ID {
			return list
		}
	}
	if utf8.RuneCountInString(m.Text) > MaxOwnerMessageText {
		m.Text = string([]rune(m.Text)[:MaxOwnerMessageText])
	}
	list = append(list, m)
	if len(list) > MaxOwnerMessages {
		list = append([]OwnerMessage(nil), list[len(list)-MaxOwnerMessages:]...)
	}
	return list
}

// PendingOwnerMessages are the owner messages not in status.run.deliveredMessages.
func (t *AgentTask) PendingOwnerMessages() []OwnerMessage {
	var out []OwnerMessage
	for _, m := range t.Spec.Source.OwnerMessages {
		if t.Delivery(m.ID) == nil {
			out = append(out, m)
		}
	}
	return out
}

// Delivery is the record of owner message id, or nil if it is not delivered.
func (t *AgentTask) Delivery(id string) *DeliveredMessage {
	for i := range t.Status.Run.DeliveredMessages {
		if t.Status.Run.DeliveredMessages[i].ID == id {
			return &t.Status.Run.DeliveredMessages[i]
		}
	}
	return nil
}
