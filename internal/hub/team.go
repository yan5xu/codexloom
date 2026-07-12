package hub

import "sort"

type TeamView struct {
	Agents        []TeamAgent        `json:"agents"`
	ObservedLinks []TeamObservedLink `json:"observedLinks"`
	ExplicitLinks []TeamRelationship `json:"explicitLinks"`
}

type TeamAgent struct {
	Name          string       `json:"name"`
	ID            string       `json:"id"`
	Cwd           string       `json:"cwd,omitempty"`
	Status        string       `json:"status,omitempty"`
	Source        string       `json:"source,omitempty"`
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
		agents[s.ID] = &TeamAgent{
			Name: s.Name, ID: s.ID, Cwd: s.Cwd, Status: s.Status, Source: s.Source,
			Profile: profileCopy(h.profiles[s.ID], s.ID),
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
	h.mu.Unlock()

	links := map[string]*TeamObservedLink{}
	roots := map[string]AgentMessage{}
	for _, msg := range messages {
		msg.FromAgentID = teamAgentID(agents, msg.FromAgentID, msg.From)
		msg.ToAgentID = teamAgentID(agents, msg.ToAgentID, msg.To)
		from := ensureTeamAgent(agents, msg.FromAgentID, msg.From)
		to := ensureTeamAgent(agents, msg.ToAgentID, msg.To)
		from.MessageOut++
		to.MessageIn++
		mergeLastMessageAt(from, msg.CreatedAt)
		mergeLastMessageAt(to, msg.CreatedAt)
		if msg.FromAgentID == schedulerAgentID {
			to.ScheduledIn++
		}
		if msg.Status == "open" {
			from.OpenOut++
			to.OpenIn++
		}
		if msg.ReplyTo == "" {
			roots[msg.ID] = msg
			link := ensureObservedLink(links, msg.FromAgentID, msg.ToAgentID, from.Name, to.Name)
			link.MessageCount++
			switch msg.Status {
			case "open":
				link.OpenCount++
			case "answered":
				link.AnsweredCount++
			case "closed":
				link.ClosedCount++
			}
			if msg.DeliveryStatus == "queued" || msg.DeliveryStatus == "delivering" {
				link.QueuedCount++
			} else if msg.DeliveryStatus == "failed" {
				link.FailedCount++
			}
			mergeLinkLastMessageAt(link, msg.CreatedAt)
			addLinkSubject(link, msg.Subject)
		}
	}
	for _, msg := range messages {
		if msg.ReplyTo == "" {
			continue
		}
		if root, ok := roots[msg.ReplyTo]; ok {
			link := ensureObservedLink(links, root.FromAgentID, root.ToAgentID, root.From, root.To)
			link.ReplyCount++
			if msg.CreatedAt > link.LastReplyAt {
				link.LastReplyAt = msg.CreatedAt
			}
		}
	}
	for _, rel := range explicit {
		ensureTeamAgent(agents, rel.FromAgentID, rel.From)
		ensureTeamAgent(agents, rel.ToAgentID, rel.To)
	}

	outAgents := make([]TeamAgent, 0, len(agents))
	for _, agent := range agents {
		outAgents = append(outAgents, *agent)
	}
	sort.SliceStable(outAgents, func(i, j int) bool {
		if outAgents[i].Status == "running" && outAgents[j].Status != "running" {
			return true
		}
		if outAgents[i].Status != "running" && outAgents[j].Status == "running" {
			return false
		}
		if outAgents[i].MessageIn+outAgents[i].MessageOut != outAgents[j].MessageIn+outAgents[j].MessageOut {
			return outAgents[i].MessageIn+outAgents[i].MessageOut > outAgents[j].MessageIn+outAgents[j].MessageOut
		}
		return outAgents[i].Name < outAgents[j].Name
	})

	outLinks := make([]TeamObservedLink, 0, len(links))
	for _, link := range links {
		outLinks = append(outLinks, *link)
	}
	sort.SliceStable(outLinks, func(i, j int) bool {
		if outLinks[i].LastMessageAt != outLinks[j].LastMessageAt {
			return outLinks[i].LastMessageAt > outLinks[j].LastMessageAt
		}
		if outLinks[i].MessageCount != outLinks[j].MessageCount {
			return outLinks[i].MessageCount > outLinks[j].MessageCount
		}
		return outLinks[i].FromAgentID < outLinks[j].FromAgentID
	})
	sort.SliceStable(explicit, func(i, j int) bool {
		if explicit[i].UpdatedAt != explicit[j].UpdatedAt {
			return explicit[i].UpdatedAt > explicit[j].UpdatedAt
		}
		return explicit[i].ID < explicit[j].ID
	})

	return TeamView{Agents: outAgents, ObservedLinks: outLinks, ExplicitLinks: explicit}
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
