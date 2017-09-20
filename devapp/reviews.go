package main

import (
	"bytes"
	"html/template"
	"io"
	"log"
	"net/http"
	"sort"
	"time"

	"golang.org/x/build/maintner"
)

type project struct {
	*maintner.GerritProject
	Changes []*change
}

type change struct {
	*maintner.GerritCL
	LastUpdate          time.Time
	FormattedLastUpdate string
}

type reviewsData struct {
	Projects []*project

	// dirty is set if this data needs to be updated due to a corpus change.
	dirty bool
}

// handleReviews serves dev.golang.org/reviews.
func (s *server) handleReviews(t *template.Template, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.cMu.RLock()
	dirty := s.data.reviews.dirty
	s.cMu.RUnlock()
	if dirty {
		s.updateReviewsData()
	}

	s.cMu.RLock()
	defer s.cMu.RUnlock()

	ownerFilter := r.FormValue("owner")
	var projects []*project
	if len(ownerFilter) > 0 {
		for _, p := range s.data.reviews.Projects {
			var cs []*change
			for _, c := range p.Changes {
				if c.OwnerName() == ownerFilter {
					cs = append(cs, c)
				}
			}
			if len(cs) > 0 {
				projects = append(projects, &project{GerritProject: p.GerritProject, Changes: cs})
			}
		}
	} else {
		projects = s.data.reviews.Projects
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, struct{ Projects []*project }{projects}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(w, &buf); err != nil {
		log.Printf("io.Copy(w, %+v) = %v", buf, err)
		return
	}
}

func (s *server) updateReviewsData() {
	log.Println("Updating reviews data ...")
	s.cMu.Lock()
	defer s.cMu.Unlock()

	var projects []*project
	s.corpus.Gerrit().ForeachProjectUnsorted(func(p *maintner.GerritProject) error {
		proj := &project{GerritProject: p}
		p.ForeachOpenCL(func(cl *maintner.GerritCL) error {
			c := &change{GerritCL: cl}
			c.LastUpdate = cl.Commit.CommitTime
			if len(cl.Messages) > 0 {
				c.LastUpdate = cl.Messages[len(cl.Messages)-1].Date
			}
			c.FormattedLastUpdate = c.LastUpdate.Format("2006-01-02")
			proj.Changes = append(proj.Changes, c)
			return nil
		})
		sort.Slice(proj.Changes, func(i, j int) bool {
			return proj.Changes[i].LastUpdate.Before(proj.Changes[j].LastUpdate)
		})
		projects = append(projects, proj)
		return nil
	})
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Project() < projects[j].Project()
	})
	s.data.reviews.Projects = projects
	s.data.reviews.dirty = false
}

func (c *change) OwnerName() string {
	m := c.firstMetaCommit()
	if m == nil {
		return ""
	}
	return m.Author.Name()
}

func (c *change) firstMetaCommit() *maintner.GitCommit {
	m := c.Meta
	for m != nil && len(m.Parents) > 0 {
		m = m.Parents[0] // Meta commits don’t have more than one parent.
	}
	return m
}