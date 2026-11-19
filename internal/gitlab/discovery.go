package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GroupDiscovery is what one discovery of a group read: its projects
// (subgroups included, archived ones too), their active milestones, and the
// group's open issues with the ready label.
type GroupDiscovery struct {
	Projects    []DiscoveredProject
	ReadyIssues []DiscoveredIssue
	// Complexity is the highest query complexity GitLab reported over the POSTs.
	Complexity QueryComplexity
	// Posts is the number of GraphQL queries sent, retries not counted.
	Posts int
}

// DiscoveredProject is a project of the group with its first
// DiscoveryMilestones active milestones. Archived projects are returned; the
// caller filters them out.
type DiscoveredProject struct {
	FullPath   string
	Archived   bool
	Milestones []DiscoveredMilestone
	// MilestonesTruncated: the project may have more active milestones than
	// Milestones holds.
	MilestonesTruncated bool
}

// DiscoveryMilestones is how many active milestones discovery reads per
// project.
const DiscoveryMilestones = 20

// DiscoveredMilestone is an active milestone of a discovered project.
type DiscoveredMilestone struct {
	IID                int64
	Title, Description string
	UpdatedAt          time.Time
}

// DiscoveredIssue is an open issue with the ready label: its project's full
// path and its iid.
type DiscoveredIssue struct {
	Project string
	IID     int64
}

// QueryComplexity is GitLab's score of a GraphQL query and its cap.
type QueryComplexity struct {
	Score, Limit int
}

// Discoverer reads a whole GitLab group in a few GraphQL queries.
type Discoverer interface {
	DiscoverGroup(ctx context.Context, group, readyLabel string) (*GroupDiscovery, error)
}

var _ Discoverer = (*Client)(nil)

// GraphQLError is a GraphQL answer with errors. It is not transient: the
// query or the token's permissions are wrong.
type GraphQLError struct {
	Messages []string
}

func (e *GraphQLError) Error() string {
	return "gitlab: graphql: " + strings.Join(e.Messages, "; ")
}

// maxDiscoveryPosts bounds the queries of one discovery: 50 pages of 100
// projects.
const maxDiscoveryPosts = 50

const graphqlPath = "/api/graphql"

type gqlPageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type gqlDiscovery struct {
	Data struct {
		QueryComplexity *QueryComplexity `json:"queryComplexity"`
		Group           *struct {
			Projects *struct {
				Nodes []struct {
					FullPath   string `json:"fullPath"`
					Archived   bool   `json:"archived"`
					Milestones struct {
						Nodes []struct {
							IID         string    `json:"iid"`
							Title       string    `json:"title"`
							Description string    `json:"description"`
							UpdatedAt   time.Time `json:"updatedAt"`
						} `json:"nodes"`
					} `json:"milestones"`
				} `json:"nodes"`
				PageInfo gqlPageInfo `json:"pageInfo"`
			} `json:"projects"`
			Issues *struct {
				Nodes []struct {
					Reference string `json:"reference"`
				} `json:"nodes"`
				PageInfo gqlPageInfo `json:"pageInfo"`
			} `json:"issues"`
		} `json:"group"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// connection is one paginated connection of the discovery query.
type connection struct {
	open   bool
	cursor string // the endCursor of the last page, "" before the first
}

// next moves c past a page, failing on a cursor that does not advance.
func (c *connection) next(name string, p gqlPageInfo) error {
	if !p.HasNextPage {
		c.open = false
		return nil
	}
	if p.EndCursor == "" || p.EndCursor == c.cursor {
		return fmt.Errorf("gitlab: graphql: %s cursor does not advance", name)
	}
	c.cursor = p.EndCursor
	return nil
}

// DiscoverGroup reads group's projects and the open issues labelled
// readyLabel (none for an empty label), 100 of each per POST. A later POST
// asks only for the connections that still have pages.
func (c *Client) DiscoverGroup(ctx context.Context, group, readyLabel string) (*GroupDiscovery, error) {
	projects := connection{open: true}
	issues := connection{open: readyLabel != ""}
	out := &GroupDiscovery{}
	for projects.open || issues.open {
		if out.Posts >= maxDiscoveryPosts {
			return nil, fmt.Errorf("gitlab: graphql: discovery of %s needs more than %d queries", group, maxDiscoveryPosts)
		}
		query, vars := discoveryQuery(group, readyLabel, projects, issues)
		payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
		if err != nil {
			return nil, err
		}
		var res gqlDiscovery
		out.Posts++
		if _, err := c.call(ctx, request{method: http.MethodPost, url: c.graphqlURL(), path: graphqlPath, payload: payload, safe: true, graphql: true}, &res); err != nil {
			return nil, err
		}
		if len(res.Errors) > 0 {
			e := &GraphQLError{}
			for _, m := range res.Errors[:min(len(res.Errors), 3)] {
				e.Messages = append(e.Messages, m.Message)
			}
			return nil, e
		}
		if qc := res.Data.QueryComplexity; qc != nil && qc.Score >= out.Complexity.Score {
			out.Complexity = *qc
		}
		g := res.Data.Group
		if g == nil {
			return nil, &Error{StatusCode: http.StatusNotFound, Method: http.MethodPost, Path: graphqlPath,
				Body: fmt.Sprintf("group %s not found or not visible", group)}
		}
		if projects.open {
			if g.Projects == nil {
				return nil, errors.New("gitlab: graphql: answer without projects")
			}
			for _, n := range g.Projects.Nodes {
				p := DiscoveredProject{FullPath: n.FullPath, Archived: n.Archived}
				for _, m := range n.Milestones.Nodes {
					iid, err := strconv.ParseInt(m.IID, 10, 64)
					if err != nil {
						return nil, fmt.Errorf("gitlab: graphql: milestone iid %q of %s: %w", m.IID, n.FullPath, err)
					}
					p.Milestones = append(p.Milestones, DiscoveredMilestone{IID: iid, Title: m.Title, Description: m.Description, UpdatedAt: m.UpdatedAt})
				}
				p.MilestonesTruncated = len(p.Milestones) >= DiscoveryMilestones
				out.Projects = append(out.Projects, p)
			}
			if err := projects.next("projects", g.Projects.PageInfo); err != nil {
				return nil, err
			}
		}
		if issues.open {
			if g.Issues == nil {
				return nil, errors.New("gitlab: graphql: answer without issues")
			}
			for _, n := range g.Issues.Nodes {
				is, err := parseIssueReference(n.Reference)
				if err != nil {
					return nil, err
				}
				out.ReadyIssues = append(out.ReadyIssues, is)
			}
			if err := issues.next("issues", g.Issues.PageInfo); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// graphqlURL is the GraphQL endpoint next to the REST API.
func (c *Client) graphqlURL() string {
	return strings.TrimSuffix(c.baseURL, "/api/v4") + graphqlPath
}

// discoveryQuery builds the query for the connections still open, with the
// group, label and cursors as variables.
func discoveryQuery(group, label string, projects, issues connection) (string, map[string]any) {
	params := []string{"$group: ID!"}
	vars := map[string]any{"group": group}
	var fields []string
	if projects.open {
		params = append(params, "$pCursor: String")
		vars["pCursor"] = nullable(projects.cursor)
		fields = append(fields, `projects(includeSubgroups: true, first: 100, after: $pCursor) {
      nodes { fullPath archived milestones(state: active, first: 20) { nodes { iid title description updatedAt } } }
      pageInfo { hasNextPage endCursor }
    }`)
	}
	if issues.open {
		params = append(params, "$label: String!", "$iCursor: String")
		vars["label"] = label
		vars["iCursor"] = nullable(issues.cursor)
		fields = append(fields, `issues(includeSubgroups: true, state: opened, labelName: [$label], first: 100, after: $iCursor) {
      nodes { reference(full: true) }
      pageInfo { hasNextPage endCursor }
    }`)
	}
	q := "query(" + strings.Join(params, ", ") + ") {\n  queryComplexity { score limit }\n  group(fullPath: $group) {\n    " +
		strings.Join(fields, "\n    ") + "\n  }\n}"
	return q, vars
}

// nullable is nil for the first page's empty cursor.
func nullable(cursor string) any {
	if cursor == "" {
		return nil
	}
	return cursor
}

// parseIssueReference splits a full issue reference, group/sub/proj#12.
func parseIssueReference(ref string) (DiscoveredIssue, error) {
	i := strings.LastIndexByte(ref, '#')
	if i <= 0 {
		return DiscoveredIssue{}, fmt.Errorf("gitlab: graphql: issue reference %q", ref)
	}
	iid, err := strconv.ParseInt(ref[i+1:], 10, 64)
	if err != nil || iid <= 0 {
		return DiscoveredIssue{}, fmt.Errorf("gitlab: graphql: issue reference %q", ref)
	}
	return DiscoveredIssue{Project: ref[:i], IID: iid}, nil
}
