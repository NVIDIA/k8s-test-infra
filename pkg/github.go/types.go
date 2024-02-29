/*
 * Copyright (c) 2024, NVIDIA CORPORATION.  All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package github

import "time"

// IssueCommentEventAction enumerates the triggers for this
// webhook payload type. See also:
// https://developer.github.com/v3/activity/events/types/#issuecommentevent
type IssueCommentEventAction string

const (
	// IssueCommentActionCreated means the comment was created.
	IssueCommentActionCreated IssueCommentEventAction = "created"
	// IssueCommentActionEdited means the comment was edited.
	IssueCommentActionEdited IssueCommentEventAction = "edited"
	// IssueCommentActionDeleted means the comment was deleted.
	IssueCommentActionDeleted IssueCommentEventAction = "deleted"
)

// IssueCommentEvent is what GitHub sends us when an issue comment is changed.
type IssueCommentEvent struct {
	Action  IssueCommentEventAction `json:"action"`
	Issue   Issue                   `json:"issue"`
	Comment IssueComment            `json:"comment"`
	Repo    Repo                    `json:"repository"`

	// GUID is included in the header of the request received by GitHub.
	GUID string
}

// Repo contains general repository information: it includes fields available
// in repo records returned by GH "List" methods but not those returned by GH
// "Get" method. Use FullRepo struct for "Get" method.
// See also https://developer.github.com/v3/repos/#list-organization-repositories
type Repo struct {
	Owner         User   `json:"owner"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	HTMLURL       string `json:"html_url"`
	Fork          bool   `json:"fork"`
	DefaultBranch string `json:"default_branch"`
	Archived      bool   `json:"archived"`
	Private       bool   `json:"private"`
	Description   string `json:"description"`
	Homepage      string `json:"homepage"`
	HasIssues     bool   `json:"has_issues"`
	HasProjects   bool   `json:"has_projects"`
	HasWiki       bool   `json:"has_wiki"`
	NodeID        string `json:"node_id"`
	// Permissions reflect the permission level for the requester, so
	// on a repository GET call this will be for the user whose token
	// is being used, if listing a team's repos this will be for the
	// team's privilege level in the repo
	Permissions RepoPermissions `json:"permissions"`
	Parent      ParentRepo      `json:"parent"`
}

// ParentRepo contains a small subsection of general repository information: it
// just includes the information needed to confirm that a parent repo exists
// and what the name of that repo is.
type ParentRepo struct {
	Owner    User   `json:"owner"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
}

// Repo contains detailed repository information, including items
// that are not available in repo records returned by GH "List" methods
// but are in those returned by GH "Get" method.
// See https://developer.github.com/v3/repos/#list-organization-repositories
// See https://developer.github.com/v3/repos/#get
type FullRepo struct {
	Repo

	AllowSquashMerge         bool   `json:"allow_squash_merge,omitempty"`
	AllowMergeCommit         bool   `json:"allow_merge_commit,omitempty"`
	AllowRebaseMerge         bool   `json:"allow_rebase_merge,omitempty"`
	SquashMergeCommitTitle   string `json:"squash_merge_commit_title,omitempty"`
	SquashMergeCommitMessage string `json:"squash_merge_commit_message,omitempty"`
}

// Issue represents general info about an issue.
type Issue struct {
	ID          int       `json:"id"`
	NodeID      string    `json:"node_id"`
	User        User      `json:"user"`
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	State       string    `json:"state"`
	HTMLURL     string    `json:"html_url"`
	Labels      []Label   `json:"labels"`
	Assignees   []User    `json:"assignees"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Milestone   Milestone `json:"milestone"`
	StateReason string    `json:"state_reason"`

	// This will be non-nil if it is a pull request.
	PullRequest *struct{} `json:"pull_request,omitempty"`
}

// Label describes a GitHub label.
type Label struct {
	URL         string `json:"url"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
}

// Milestone is a milestone defined on a github repository
type Milestone struct {
	Title  string `json:"title"`
	Number int    `json:"number"`
	State  string `json:"state"`
}

// User is a GitHub user account.
type User struct {
	Login       string          `json:"login"`
	Name        string          `json:"name"`
	Email       string          `json:"email"`
	ID          int             `json:"id"`
	HTMLURL     string          `json:"html_url"`
	Permissions RepoPermissions `json:"permissions"`
	Type        string          `json:"type"`
}

const (
	// UserTypeUser identifies an actual user account in the User.Type field
	UserTypeUser = "User"
	// UserTypeBot identifies a github app bot user in the User.Type field
	UserTypeBot = "Bot"
)

// RepoPermissions describes which permission level an entity has in a
// repo. At most one of the booleans here should be true.
type RepoPermissions struct {
	// Pull is equivalent to "Read" permissions in the web UI
	Pull   bool `json:"pull"`
	Triage bool `json:"triage"`
	// Push is equivalent to "Edit" permissions in the web UI
	Push     bool `json:"push"`
	Maintain bool `json:"maintain"`
	Admin    bool `json:"admin"`
}

// IssueComment represents general info about an issue comment.
type IssueComment struct {
	ID        int       `json:"id,omitempty"`
	Body      string    `json:"body"`
	User      User      `json:"user,omitempty"`
	HTMLURL   string    `json:"html_url,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}
