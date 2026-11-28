// SPEC: _spec/internal/dashboard/workspace-instances.puml
package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type taskMetadata struct {
	id, key, excerpt string
	pr               prInfo   // the first PR, with every PR's tickets and the first plan; or why none was read
	prs              []prInfo // every PR the task's branches have
	branches         string   // the working branches the PRs were read for
	linear           linearIssue
	plan             planInfo            // the plan the task follows, with what it waits on
	planRefs         map[string]planInfo // the plans it waits on, by graph key
	at               time.Time
}

type linearIssue struct {
	Identifier, Title, Description, URL, CreatedAt string
	BlockedBy                                      []linearRef // the open issues Linear says block this one
}

// linearRef is another issue as a relation names it.
type linearRef struct{ Identifier, Title, URL, State, StateType string }

var linearID = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)
var linearIDInLabel = regexp.MustCompile(`(?i)\b[A-Z][A-Z0-9]*-[0-9]+\b`)

func linearIdentifier(t Task) string {
	if linearID.MatchString(t.Ticket) {
		return t.Ticket
	}
	if u, err := url.Parse(t.Ticket); err == nil && u.Scheme == "https" && u.Host == "linear.app" {
		_, tail, _ := strings.Cut(u.Path, "/issue/")
		id, _, _ := strings.Cut(tail, "/")
		return id
	}
	if t.Ticket != "" {
		return ""
	}
	for _, text := range []string{t.Branch, t.Title} {
		if id := linearIDInLabel.FindString(text); id != "" {
			return strings.ToUpper(id)
		}
	}
	return ""
}

func metadataKey(t Task) string {
	return t.ID + "\x00" + t.Ticket + "\x00" + t.Repo + "\x00" + t.Branch + "\x00" + t.Tip
}

func (m *dashModel) readMetadata(t Task) tea.Cmd {
	linear := linearIdentifier(t)
	if t.ID == "" || m.metadataPending || m.sbxTab || t.Review != nil {
		return nil
	}
	cacheKey := metadataKey(t)
	if cached, ok := m.metadata[cacheKey]; ok && time.Since(cached.at) < time.Minute {
		return nil
	}
	cwd, plans := m.CWD, m.planSource()
	m.metadataPending = true
	return func() tea.Msg {
		meta := taskMetadata{id: t.ID, key: cacheKey, at: time.Now(), branches: strings.Join(t.Branches, ",")}
		meta.excerpt = issueExcerpt(t.Ticket)
		meta.prs, meta.pr = branchPRs(t, cwd)
		meta.plan, meta.planRefs = readTaskPlan(cwd, plans, t.Ticket, meta.pr.Plan)
		if linear == "" && len(meta.pr.Tickets) > 0 {
			linear = meta.pr.Tickets[0]
		}
		if linear != "" {
			meta.linear = readLinearIssue(linearToken(cwd), linear)
			if meta.linear.Description != "" {
				meta.excerpt = meta.linear.Description
			}
		}
		return meta
	}
}

func issueExcerpt(ticket string) string {
	u, err := url.Parse(ticket)
	if err != nil || u.Scheme != "https" {
		return ""
	}
	switch u.Host {
	case "github.com":
		b, err := providerCommand("", "gh", "issue", "view", u.String(), "--json", "body")
		var v struct{ Body string }
		if err == nil && json.Unmarshal(b, &v) == nil {
			return v.Body
		}
	}
	return ""
}

func readLinearIssue(token, id string) linearIssue {
	if token == "" || id == "" {
		return linearIssue{}
	}
	var v struct {
		Issue struct {
			linearIssue
			InverseRelations struct {
				Nodes []struct {
					Type  string
					Issue struct {
						Identifier, Title, URL string
						State                  struct{ Name, Type string }
					}
				}
			}
		}
	}
	if linearQuery(token, linearIssueQuery, map[string]string{"id": id}, 3*time.Second, &v) != nil {
		return linearIssue{}
	}
	out := v.Issue.linearIssue
	for _, n := range v.Issue.InverseRelations.Nodes {
		if s := n.Issue.State.Type; n.Type == "blocks" && s != "completed" && s != "canceled" {
			out.BlockedBy = append(out.BlockedBy, linearRef{Identifier: n.Issue.Identifier, Title: n.Issue.Title, URL: n.Issue.URL,
				State: n.Issue.State.Name, StateType: s})
		}
	}
	return out
}

// linearToken is LINEAR_API_KEY from the dashboard's environment, else the workspace .env, which proveo keeps host-only.
func linearToken(cwd string) string { return tokenFor(cwd, "LINEAR_API_KEY") }

// linearQuery posts one GraphQL query to Linear and decodes its data into out.
var linearQuery = func(token, query string, vars any, timeout time.Duration, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.linear.app/graphql", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var v struct {
		Data   json.RawMessage
		Errors []struct{ Message string }
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v); err != nil {
		return fmt.Errorf("linear answered %s", resp.Status)
	}
	if len(v.Errors) > 0 {
		return errors.New("linear: " + v.Errors[0].Message)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("linear answered %s", resp.Status)
	}
	return json.Unmarshal(v.Data, out)
}

// branchPRs is every PR of the task's branches, and the first one with all their tickets and the first plan.
func branchPRs(t Task, cwd string) ([]prInfo, prInfo) {
	if t.Git == "GitLab" {
		pr := gitlabMR(t)
		if pr.Number == 0 {
			return nil, pr
		}
		return []prInfo{pr}, pr
	}
	prs, note := taskPRs(t, cwd)
	if len(prs) == 0 {
		return nil, prInfo{Note: note}
	}
	first := prs[0]
	for _, p := range prs[1:] {
		for _, id := range p.Tickets {
			if !slices.Contains(first.Tickets, id) {
				first.Tickets = append(first.Tickets, id)
			}
		}
		if first.Plan == "" {
			first.Plan = p.Plan
		}
	}
	return prs, first
}

func gitlabMR(t Task) prInfo {
	switch t.Git {
	case "GitLab":
		b, err := providerCommand(t.Repo, "glab", "mr", "list", "--source-branch", t.Branch, "--all", "--output", "json")
		var p []struct {
			IID   int
			Draft bool
		}
		if err == nil && json.Unmarshal(b, &p) == nil && len(p) > 0 {
			return prInfo{Number: p[0].IID, State: "OPEN", Draft: p[0].Draft}
		}
		return prInfo{}
	}
	return prInfo{}
}

func providerCommand(dir, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GLAB_NO_PROMPT=1")
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

// linearIssueQuery reads one issue and the relations that point at it: an inverse "blocks" is a blocker.
const linearIssueQuery = `query($id: String!) { issue(id: $id) { identifier title description url createdAt
  inverseRelations(first: 50) { nodes { type issue { identifier title url state { name type } } } } } }`
