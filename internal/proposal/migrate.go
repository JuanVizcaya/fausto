package proposal

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/JuanVizcaya/fausto/internal/edit"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
)

// Where fausto kept its workflow state in git before the database.
const (
	legacyTasksDir   = "tasks"
	legacyRecordsDir = spec.Dir + "/proposals"
)

// tasksReadme replaces tasks/README.md once the tasks have moved.
const tasksReadme = `# Agent tasks

Tasks no longer live here. fausto keeps them in its database, and mirrors
them as Jira issues (with the task's .md attached) when Jira is configured.

- People see and update their tasks in fausto's Tasks tab.
- An agent downloads its open tasks with ` + "`fausto task pull <agent>`" + `, which
  writes them to the git-ignored ` + "`.fausto/tasks/`" + `, and reports progress
  with ` + "`fausto task start <id>`" + ` and ` + "`fausto task done <id>`" + `.

See https://github.com/JuanVizcaya/fausto#storage.
`

// Migration is what Migrate found and did.
type Migration struct {
	Proposals, Tasks []string // ids imported
	Skipped          []string // ids already in the database
	Removed          []string // files removed from the base branch
	Commit           string   // the commit removing them; "" on a dry run
}

// Migrate moves the proposals recorded in system/proposals/ and the tasks
// in tasks/ on the base branch into the database, then removes those
// files with one commit. Ids already in the database are left alone, so
// it is safe to run again. With dryRun it only reports what it would do.
func (s *Store) Migrate(ctx context.Context, dryRun bool) (*Migration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	baseHead, err := s.revParse("refs/heads/" + s.Base)
	if err != nil {
		return nil, err
	}
	names, err := s.git("", nil, nil, "ls-tree", "-r", "--name-only", baseHead, "--", legacyTasksDir+"/", legacyRecordsDir+"/")
	if err != nil {
		return nil, err
	}
	m := &Migration{}
	var proposals []store.Proposal
	var tasks []store.Task
	for _, name := range strings.Fields(names) {
		data, err := s.gitBytes("", nil, nil, "show", baseHead+":"+name)
		if err != nil {
			return nil, err
		}
		switch {
		case strings.HasPrefix(name, legacyRecordsDir+"/") && strings.HasSuffix(name, ".yaml"):
			p, err := s.legacyProposal(baseHead, data)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			proposals = append(proposals, *p)
		case strings.HasPrefix(name, legacyTasksDir+"/") && strings.HasSuffix(name, ".md") && !strings.EqualFold(path.Base(name), "README.md"):
			t, err := store.ParseTask(data)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			if t.Status == "" {
				t.Status = "open"
			}
			if err := store.ValidStatus(t.Status, store.TaskStatuses); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			tasks = append(tasks, *t)
		default:
			continue
		}
		m.Removed = append(m.Removed, name)
	}

	for i := range proposals {
		p := &proposals[i]
		if _, err := s.DB.Proposal(ctx, p.ID); err == nil {
			m.Skipped = append(m.Skipped, p.ID)
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if !dryRun {
			if err := s.DB.CreateProposal(ctx, p); err != nil && !errors.Is(err, store.ErrExists) {
				return nil, err
			}
		}
		m.Proposals = append(m.Proposals, p.ID)
	}
	for _, t := range tasks {
		if _, err := s.DB.Task(ctx, t.ID); err == nil {
			m.Skipped = append(m.Skipped, t.ID)
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if !dryRun {
			if err := s.DB.CreateTasks(ctx, []store.Task{t}); err != nil && !errors.Is(err, store.ErrExists) {
				return nil, err
			}
		}
		m.Tasks = append(m.Tasks, t.ID)
	}
	if dryRun || len(m.Removed) == 0 {
		return m, nil
	}

	files := edit.Files{legacyTasksDir + "/README.md": []byte(tasksReadme)}
	for _, name := range m.Removed {
		files[name] = nil
	}
	msg := fmt.Sprintf("Move fausto's proposals and tasks to its database\n\n"+
		"The %d proposal records and %d tasks are in fausto's database now;\ngit keeps only the spec.\n", len(proposals), len(tasks))
	if m.Commit, err = s.commit(baseHead+"^{tree}", files, []string{baseHead}, msg); err != nil {
		return nil, err
	}
	if err := s.advanceBase(m.Commit, baseHead); err != nil {
		return nil, err
	}
	return m, nil
}

// legacyProposal reads a system/proposals/<id>.yaml record. Its edits
// weren't kept, but the commit that accepted it is found by its
// "Proposal: <id>" trailer, so its review still shows what changed.
func (s *Store) legacyProposal(baseHead string, data []byte) (*store.Proposal, error) {
	var r struct {
		ID          string   `yaml:"id"`
		Title       string   `yaml:"title"`
		Description string   `yaml:"description"`
		Author      string   `yaml:"author"`
		Created     string   `yaml:"created"`
		Status      string   `yaml:"status"`
		Milestone   string   `yaml:"milestone"`
		Accepted    string   `yaml:"accepted"`
		Tasks       []string `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.ID == "" {
		return nil, errors.New("record has no id")
	}
	p := &store.Proposal{ID: r.ID, Title: r.Title, Description: r.Description, Author: r.Author, Created: r.Created,
		Status: r.Status, Milestone: r.Milestone, Accepted: r.Accepted, Tasks: r.Tasks, Legacy: true}
	if p.Tasks == nil {
		p.Tasks = []string{}
	}
	if p.Status == "accepted" {
		commit, err := s.git("", nil, nil, "log", "-1", "--format=%H", "--fixed-strings", "--grep=Proposal: "+r.ID, baseHead)
		if err == nil {
			p.Commit = strings.TrimSpace(commit)
		}
	} else {
		// An open record from git has no edits to apply; close it.
		p.Status = "rejected"
	}
	return p, nil
}
