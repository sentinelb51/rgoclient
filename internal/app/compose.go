package app

import (
	"log"
	"os"

	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui"
)

// handleSubmit sends the composed message, its attachments, and its replies.
// The composer is cleared immediately and the send runs in the background: the
// message appears when the gateway echoes it back.
func (a *App) handleSubmit(text string) {
	if (text == "" && len(a.input.Attachments) == 0) || a.currentChannelID == "" || a.session == nil {
		return
	}

	session := a.session
	channelID := a.currentChannelID
	attachments := append([]ui.Attachment(nil), a.input.Attachments...)
	replies := append([]ui.Reply(nil), a.input.Replies...)

	a.input.SetText("")
	a.input.ClearAttachments()
	a.input.ClearReplies()
	a.jumpToLatest()

	go func() {
		send := revoltgo.MessageSend{
			Content:     text,
			Attachments: uploadAttachments(session, attachments),
			Replies:     toMessageReplies(replies),
		}
		if _, err := session.ChannelMessageSend(channelID, send); err != nil {
			log.Printf("send message: %v", err)
		}
	}()
}

// uploadAttachments uploads each local file and returns the resulting IDs. A
// file that fails to open or upload is logged and skipped, so one bad
// attachment doesn't sink the whole message.
func uploadAttachments(session *revoltgo.Session, attachments []ui.Attachment) []string {
	ids := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		file, err := os.Open(attachment.Path)
		if err != nil {
			log.Printf("open attachment %s: %v", attachment.Path, err)
			continue
		}

		uploaded, err := session.AttachmentUpload(&revoltgo.FileParams{Name: attachment.Name, Reader: file})
		_ = file.Close()
		if err != nil {
			log.Printf("upload attachment %s: %v", attachment.Name, err)
			continue
		}
		ids = append(ids, uploaded.ID)
	}
	return ids
}

// toMessageReplies converts composer replies to the API representation.
func toMessageReplies(replies []ui.Reply) []*revoltgo.MessageReplies {
	out := make([]*revoltgo.MessageReplies, len(replies))
	for i, r := range replies {
		out[i] = &revoltgo.MessageReplies{ID: r.ID, Mention: r.Mention}
	}
	return out
}
