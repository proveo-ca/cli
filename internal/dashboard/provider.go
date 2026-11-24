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
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type taskMetadata struct {
	id, key, excerpt, pr string
	at                   time.Time
}

func metadataKey(t Task) string {
	return t.Ticket + "\x00" + t.Repo + "\x00" + t.Branch
}

func (m *dashModel) readMetadata(t Task) tea.Cmd {
	if t.ID == "" || (t.Ticket == "" && t.Branch == "") || m.metadataPending || !m.details || m.sbxTab {
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
	case "linear.app":
		return linearExcerpt(u)
	case "github.com":
		b, err := providerCommand("", "gh", "issue", "view", u.String(), "--json", "body")
		var v struct{ Body string }
		if err == nil && json.Unmarshal(b, &v) == nil {
			return v.Body
		}
	}
	return ""
}

func linearExcerpt(u *url.URL) string {
	token := os.Getenv("LINEAR_API_KEY")
	_, tail, ok := strings.Cut(u.Path, "/issue/")
	if token == "" || !ok {
		return ""
	}
	id, _, _ := strings.Cut(tail, "/")
	body, _ := json.Marshal(map[string]any{"query": "query($id: String!) { issue(id: $id) { description } }", "variables": map[string]string{"id": id}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.linear.app/graphql", bytes.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var v struct {
		Data struct{ Issue struct{ Description string } }
	}
	if resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 128*1024)).Decode(&v) == nil {
		return v.Data.Issue.Description
	}
	return ""
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
