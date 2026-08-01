package app

import (
	"slices"
	"strings"

	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/ui"
	"RGOClient/internal/util"
)

// refreshMentionCandidates hands the composer's @picker the people mentionable
// in the open channel. The picker snapshots the list and filters that snapshot
// on every keystroke, so this is where the (comparatively expensive) State walk
// and name resolution happen — once per membership change, not once per key.
//
// Call on the UI thread, whenever the open channel or its membership changes.
func (a *App) refreshMentionCandidates() {
	if a.input == nil {
		return
	}
	a.input.Mentions.SetCandidates(a.mentionCandidates())
}

// mentionCandidates resolves the mentionable people in the open channel from
// State alone — no network, the same rule the member sidebar follows. In a
// server that means whoever State knows: the gateway's members plus the ones
// lazy author resolution has pulled in (see ensureAuthor), which is exactly the
// set of people already visible in the channel. In a DM or group it is the
// channel's recipients.
func (a *App) mentionCandidates() []ui.MentionCandidate {
	if a.session == nil || a.currentChannelID == "" {
		return nil
	}

	if serverID := a.channelServerID(a.currentChannelID); serverID != "" {
		members := a.session.State.Members(serverID)
		candidates := make([]ui.MentionCandidate, 0, len(members))
		for _, member := range members {
			if candidate, ok := a.memberCandidate(member); ok {
				candidates = append(candidates, candidate)
			}
		}
		return sortCandidates(candidates)
	}

	channel := a.currentChannel()
	if channel == nil {
		return nil
	}
	candidates := make([]ui.MentionCandidate, 0, len(channel.Recipients))
	for _, userID := range channel.Recipients {
		if candidate, ok := a.userCandidate(userID); ok {
			candidates = append(candidates, candidate)
		}
	}
	return sortCandidates(candidates)
}

// memberCandidate builds a candidate from a server member, carrying the same
// nickname, per-server avatar and role colour the member sidebar shows, so the
// picker looks like the list the user is picking from. It reports false for a
// member whose user State hasn't resolved and who has no nickname either —
// there would be nothing to display or match against.
func (a *App) memberCandidate(member *revoltgo.ServerMember) (ui.MentionCandidate, bool) {
	userID := member.ID.User
	user := a.session.State.User(userID)
	if user == nil && (member.Nickname == nil || *member.Nickname == "") {
		return ui.MentionCandidate{}, false
	}

	var username string
	if user != nil {
		username = user.Username
	}
	return ui.NewMentionCandidate(
		userID,
		util.MemberName(a.session, member),
		username,
		util.MemberAvatarURL(a.session, member),
		util.MemberColor(a.session, member),
	), true
}

// userCandidate builds a candidate for a DM or group recipient, who has no
// member record and so no nickname or role colour.
func (a *App) userCandidate(userID string) (ui.MentionCandidate, bool) {
	user := a.session.State.User(userID)
	if user == nil {
		return ui.MentionCandidate{}, false
	}
	return ui.NewMentionCandidate(
		userID,
		util.UserName(a.session, userID),
		user.Username,
		user.AvatarURL("256"),
		nil,
	), true
}

// sortCandidates orders the list by display name, case-insensitively. State
// hands members back in map order, so without this the picker's suggestions
// would shuffle every time the list was rebuilt.
func sortCandidates(candidates []ui.MentionCandidate) []ui.MentionCandidate {
	// Sort on a precomputed key: lowering the name inside the comparator would
	// redo that work O(n log n) times on a large server.
	type entry struct {
		candidate ui.MentionCandidate
		key       string
	}
	entries := make([]entry, len(candidates))
	for i, candidate := range candidates {
		entries[i] = entry{candidate, strings.ToLower(candidate.Name)}
	}
	slices.SortFunc(entries, func(x, y entry) int { return strings.Compare(x.key, y.key) })

	for i, e := range entries {
		candidates[i] = e.candidate
	}
	return candidates
}
