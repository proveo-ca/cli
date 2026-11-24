// SPEC: _spec/_experiments/dashboard/workspace-instances.puml
package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type taskMetadata struct {
	id, key, excerpt, pr string
	linear               linearIssue
	at                   time.Time
}

type linearIssue struct{ Identifier, Title, Description, URL string }

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
	return t.ID + "\x00" + t.Ticket + "\x00" + t.Repo + "\x00" + t.Branch
}

func (m *dashModel) readMetadata(t Task) tea.Cmd {
	linear := linearIdentifier(t)
	if t.ID == "" || (t.Ticket == "" && t.Branch == "" && linear == "") || m.metadataPending || !m.details || m.sbxTab {
		return nil
	}
	cacheKey := metadataKey(t)
	if cached, ok := m.metadata[cacheKey]; ok && time.Since(cached.at) < 2*time.Minute {
		return nil
	}
	m.metadataPending = true
	return func() tea.Msg {
		meta := taskMetadata{id: t.ID, key: cacheKey, at: time.Now()}
		meta.excerpt = issueExcerpt(t.Ticket)
		if linear != "" {
			meta.linear = readLinearIssue(linear)
			meta.excerpt = meta.linear.Description
		}
		meta.pr = branchPR(t)
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

func readLinearIssue(id string) linearIssue {
	token := os.Getenv("LINEAR_API_KEY")
	if token == "" || id == "" {
		return linearIssue{}
	}
	body, _ := json.Marshal(map[string]any{"query": "query($id: String!) { issue(id: $id) { identifier title description url } }", "variables": map[string]string{"id": id}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.linear.app/graphql", bytes.NewReader(body))
	if err != nil {
		return linearIssue{}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return linearIssue{}
	}
	defer resp.Body.Close()
	var v struct {
		Data struct{ Issue linearIssue }
	}
	if resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 128*1024)).Decode(&v) == nil {
		return v.Data.Issue
	}
	return linearIssue{}
}

func branchPR(t Task) string {
	if t.Repo == "" || t.Branch == "" {
		return ""
	}
	switch t.Git {
	case "GitHub":
		b, err := providerCommand(t.Repo, "gh", "pr", "list", "--head", t.Branch, "--state", "all", "--limit", "1", "--json", "number,isDraft")
		var p []struct {
			Number  int
			IsDraft bool
		}
		if err == nil && json.Unmarshal(b, &p) == nil && len(p) > 0 {
			if p[0].IsDraft {
				return fmt.Sprintf("Draft #%d", p[0].Number)
			}
			return fmt.Sprintf("#%d", p[0].Number)
		}
	case "GitLab":
		b, err := providerCommand(t.Repo, "glab", "mr", "list", "--source-branch", t.Branch, "--all", "--output", "json")
		var p []struct {
			IID   int
			Draft bool
		}
		if err == nil && json.Unmarshal(b, &p) == nil && len(p) > 0 {
			if p[0].Draft {
				return fmt.Sprintf("Draft !%d", p[0].IID)
			}
			return fmt.Sprintf("!%d", p[0].IID)
		}
	}
	return ""
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
