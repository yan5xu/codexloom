package hub

import (
	"sort"
	"time"
)

type TeamView struct {
	Agents             []TeamAgent                `json:"agents"`
	OrganizationLinks  []OrganizationRelationship `json:"organizationLinks"`
	CollaborationLinks []TeamRelationship         `json:"collaborationLinks"`
	ObservedLinks      []TeamObservedLink         `json:"observedLinks"`
	ExplicitLinks      []TeamRelationship         `json:"explicitLinks"` // compatibility alias
}

type TeamAgent struct {
	Name          string       `json:"name"`
	ID            string       `json:"id"`
	Cwd           string       `json:"cwd,omitempty"`
	Status        string       `json:"status,omitempty"`
	Source        string       `json:"source,omitempty"`
	Goal          *ThreadGoal  `json:"goal,omitempty"`
	Profile       AgentProfile `json:"profile"`
	MessageIn     int          `json:"messageIn"`
	MessageOut    int          `json:"messageOut"`
	OpenIn        int          `json:"openIn"`
	OpenOut       int          `json:"openOut"`
	ScheduledIn   int          `json:"scheduledIn"`
	LastMessageAt string       `json:"lastMessageAt,omitempty"`
}

type TeamObservedLink struct {
	FromAgentID   string   `json:"fromAgentId"`
	ToAgentID     string   `json:"toAgentId"`
	From          string   `json:"from"`
	To            string   `json:"to"`
	MessageCount  int      `json:"messageCount"`
	ReplyCount    int      `json:"replyCount"`
	OpenCount     int      `json:"openCount"`
	AnsweredCount int      `json:"answeredCount"`
	ClosedCount   int      `json:"closedCount"`
	QueuedCount   int      `json:"queuedCount"`
	FailedCount   int      `json:"failedCount"`
	LastMessageAt string   `json:"lastMessageAt,omitempty"`
	LastReplyAt   string   `json:"lastReplyAt,omitempty"`
	Subjects      []string `json:"subjects"`
}

func (h *Hub) Team() TeamView {
	h.mu.Lock()
	agents := map[string]*TeamAgent{}
	for _, s := range h.agents {
		goal := cloneGoal(h.goals[s.ID])
		agents[s.ID] = &TeamAgent{
			Name: s.Name, ID: s.ID, Cwd: s.Cwd, Status: s.Status, Source: s.Source,
			Goal: goal, Profile: profileCopy(h.profiles[s.ID], s.ID),
		}
	}
	for id, profile := range h.profiles {
		if agents[id] == nil {
			agents[id] = &TeamAgent{ID: id, Name: id, Status: "archived", Source: "profile", Profile: *profile}
		}
	}
	messages := make([]AgentMessage, 0, len(h.commOrder))
	for _, id := range h.commOrder {
		if msg := h.comms[id]; msg != nil {
			messages = append(messages, *msg)
		}
	}
	explicit := make([]TeamRelationship, 0, len(h.teamLinks))
	for _, rel := range h.teamLinks {
		cp := *rel
		cp.From = h.agentNameLocked(cp.FromAgentID, cp.From)
		cp.To = h.agentNameLocked(cp.ToAgentID, cp.To)
		explicit = append(explicit, cp)
	}
	organization := make([]OrganizationRelationship, 0, len(h.organizationLinks))
	for _, relationship := range h.organizationLinks {
		copy := *relationship
		copy.Parent = h.agentNameLocked(copy.ParentAgentID, copy.Parent)
		copy.Child = h.agentNameLocked(copy.ChildAgentID, copy.Child)
		organization = append(organization, copy)
	}
	h.mu.Unlock()

	outLinks := aggregateTeamMessages(agents, messages, time.Time{})
	for _, rel := range explicit {
		ensureTeamAgent(agents, rel.FromAgentID, rel.From)
		ensureTeamAgent(agents, rel.ToAgentID, rel.To)
	}
	for _, relationship := range organization {
		ensureTeamAgent(agents, relationship.ParentAgentID, relationship.Parent)
		ensureTeamAgent(agents, relationship.ChildAgentID, relationship.Child)
	}

	outAgents := make([]TeamAgent, 0, len(agents))
	for _, agent := range agents {
		outAgents = append(outAgents, *agent)
	}
	sort.SliceStable(outAgents, func(i, j int) bool {
		if teamAgentWorking(outAgents[i]) && !teamAgentWorking(outAgents[j]) {
			return true
		}
		if !teamAgentWorking(outAgents[i]) && teamAgentWorking(outAgents[j]) {
			return false
		}
		if outAgents[i].MessageIn+outAgents[i].MessageOut != outAgents[j].MessageIn+outAgents[j].MessageOut {
			return outAgents[i].MessageIn+outAgents[i].MessageOut > outAgents[j].MessageIn+outAgents[j].MessageOut
		}
		return outAgents[i].Name < outAgents[j].Name
	})

	sort.SliceStable(explicit, func(i, j int) bool {
		if explicit[i].UpdatedAt != explicit[j].UpdatedAt {
			return explicit[i].UpdatedAt > explicit[j].UpdatedAt
		}
		return explicit[i].ID < explicit[j].ID
	})
	sort.SliceStable(organization, func(i, j int) bool {
		if organization[i].Parent != organization[j].Parent {
			return organization[i].Parent < organization[j].Parent
		}
		if organization[i].Child != organization[j].Child {
			return organization[i].Child < organization[j].Child
		}
		return organization[i].ID < organization[j].ID
	})

	return TeamView{
		Agents: outAgents, OrganizationLinks: organization, CollaborationLinks: explicit,
		ObservedLinks: outLinks, ExplicitLinks: explicit,
	}
}

// TeamActivity returns message evidence for a bounded recent window. The
// directory and declared relationships stay all-time; only this projection is
// time-scoped.
func (h *Hub) TeamActivity(days int) []TeamObservedLink {
	h.mu.Lock()
	agents := map[string]*TeamAgent{}
	for _, agent := range h.agents {
		agents[agent.ID] = &TeamAgent{Name: agent.Name, ID: agent.ID, Status: agent.Status, Goal: cloneGoal(h.goals[agent.ID]), Profile: profileCopy(h.profiles[agent.ID], agent.ID)}
	}
	messages := make([]AgentMessage, 0, len(h.commOrder))
	for _, id := range h.commOrder {
		if message := h.comms[id]; message != nil {
			messages = append(messages, *message)
		}
	}
	h.mu.Unlock()
	cutoff := time.Time{}
	if days > 0 {
		cutoff = time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	}
	return aggregateTeamMessages(agents, messages, cutoff)
}

func teamAgentWorking(agent TeamAgent) bool {
	return agent.Status == "running" || agent.Goal != nil && agent.Goal.Status == GoalStatusActive
}

func aggregateTeamMessages(agents map[string]*TeamAgent, messages []AgentMessage, cutoff time.Time) []TeamObservedLink {
	links := map[string]*TeamObservedLink{}
	roots := map[string]AgentMessage{}
	for _, message := range messages {
		if message.ReplyTo != "" {
			continue
		}
		message.FromAgentID = teamAgentID(agents, message.FromAgentID, message.From)
		message.ToAgentID = teamAgentID(agents, message.ToAgentID, message.To)
		roots[message.ID] = message
	}
	for _, message := range messages {
		if !messageInActivityWindow(message.CreatedAt, cutoff) {
			continue
		}
		message.FromAgentID = teamAgentID(agents, message.FromAgentID, message.From)
		message.ToAgentID = teamAgentID(agents, message.ToAgentID, message.To)
		from := ensureTeamAgent(agents, message.FromAgentID, message.From)
		to := ensureTeamAgent(agents, message.ToAgentID, message.To)
		from.MessageOut++
		to.MessageIn++
		mergeLastMessageAt(from, message.CreatedAt)
		mergeLastMessageAt(to, message.CreatedAt)
		if message.FromAgentID == schedulerAgentID {
			to.ScheduledIn++
		}
		if message.Status == "open" {
			from.OpenOut++
			to.OpenIn++
		}
		if message.ReplyTo != "" {
			continue
		}
		link := ensureObservedLink(links, message.FromAgentID, message.ToAgentID, from.Name, to.Name)
		link.MessageCount++
		switch message.Status {
		case "open":
			link.OpenCount++
		case "answered":
			link.AnsweredCount++
		case "closed":
			link.ClosedCount++
		}
		if message.DeliveryStatus == "queued" || message.DeliveryStatus == "delivering" {
			link.QueuedCount++
		} else if message.DeliveryStatus == "failed" {
			link.FailedCount++
		}
		mergeLinkLastMessageAt(link, message.CreatedAt)
		addLinkSubject(link, message.Subject)
	}
	for _, message := range messages {
		if message.ReplyTo == "" || !messageInActivityWindow(message.CreatedAt, cutoff) {
			continue
		}
		if root, ok := roots[message.ReplyTo]; ok {
			link := ensureObservedLink(links, root.FromAgentID, root.ToAgentID, root.From, root.To)
			link.ReplyCount++
			if message.CreatedAt > link.LastReplyAt {
				link.LastReplyAt = message.CreatedAt
			}
		}
	}
	out := make([]TeamObservedLink, 0, len(links))
	for _, link := range links {
		out = append(out, *link)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastMessageAt != out[j].LastMessageAt {
			return out[i].LastMessageAt > out[j].LastMessageAt
		}
		if out[i].MessageCount != out[j].MessageCount {
			return out[i].MessageCount > out[j].MessageCount
		}
		return out[i].FromAgentID < out[j].FromAgentID
	})
	return out
}

func messageInActivityWindow(createdAt string, cutoff time.Time) bool {
	if cutoff.IsZero() {
		return true
	}
	timestamp, err := time.Parse(time.RFC3339Nano, createdAt)
	return err == nil && !timestamp.Before(cutoff)
}

func profileCopy(profile *AgentProfile, agentID string) AgentProfile {
	if profile == nil {
		return AgentProfile{AgentID: agentID}
	}
	return *profile
}

func teamAgentID(agents map[string]*TeamAgent, id, name string) string {
	if id != "" {
		return id
	}
	for agentID, agent := range agents {
		if agent.Name == name {
			return agentID
		}
	}
	if name == schedulerIdentity {
		return schedulerAgentID
	}
	return legacyAgentID(name)
}

func ensureTeamAgent(agents map[string]*TeamAgent, id, name string) *TeamAgent {
	if id == "" {
		id = legacyAgentID(name)
	}
	if agents[id] == nil {
		status := "external"
		source := "message participant"
		if id == schedulerAgentID {
			status = "system"
			source = "scheduler"
		}
		agents[id] = &TeamAgent{Name: name, ID: id, Status: status, Source: source, Profile: AgentProfile{AgentID: id}}
	}
	if agents[id].Name == "" || agents[id].Name == id {
		agents[id].Name = name
	}
	return agents[id]
}

func ensureObservedLink(links map[string]*TeamObservedLink, fromID, toID, from, to string) *TeamObservedLink {
	key := fromID + "\x00" + toID
	if links[key] == nil {
		links[key] = &TeamObservedLink{
			FromAgentID: fromID, ToAgentID: toID, From: from, To: to, Subjects: []string{},
		}
	}
	return links[key]
}

func mergeLastMessageAt(agent *TeamAgent, ts string) {
	if ts > agent.LastMessageAt {
		agent.LastMessageAt = ts
	}
}

func mergeLinkLastMessageAt(link *TeamObservedLink, ts string) {
	if ts > link.LastMessageAt {
		link.LastMessageAt = ts
	}
}

func addLinkSubject(link *TeamObservedLink, subject string) {
	if subject == "" {
		return
	}
	for _, existing := range link.Subjects {
		if existing == subject {
			return
		}
	}
	if len(link.Subjects) < 5 {
		link.Subjects = append(link.Subjects, subject)
	}
}
