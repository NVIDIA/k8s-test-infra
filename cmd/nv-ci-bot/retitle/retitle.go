/*
 * Copyright (c) 2023, NVIDIA CORPORATION.  All rights reserved.
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

package retitle

import (
	"context"
	"os"
	"regexp"
	"strings"

	"golang.org/x/oauth2"
	"gopkg.in/yaml.v2"
	"k8s.io/klog/v2"

	"github.com/google/go-github/v59/github"
	"github.com/sethvargo/go-githubactions"
	cli "github.com/urfave/cli/v2"
)

type command struct {
	log *klog.Logger
}

var (
	retitleRe = regexp.MustCompile(`(?mi)^/retitle\s*(.*)$`)
)

// NewCommand constructs the retitle command with the specified logger
func NewCommand(log *klog.Logger) *cli.Command {
	c := command{
		log: log,
	}
	return c.build()
}

func (m command) build() *cli.Command {

	// Create the 'retitle' command
	retitle := cli.Command{
		Name:  "retitle",
		Usage: "Edit the title of a GitHub issue or pull request",

		Before: func(c *cli.Context) error {
			return nil
		},
		Action: func(c *cli.Context) error {
			return m.run(c)
		},
	}

	return &retitle
}

func (m command) run(c *cli.Context) error {
	action := githubactions.New()

	// Create the GitHub client
	accessToken := os.Getenv("INPUT_GITHUB-TOKEN")
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: accessToken},
	)
	tc := oauth2.NewClient(c.Context, ts)
	gh := github.NewClient(tc)

	// Get the event
	context, err := action.Context()
	if err != nil {
		m.log.Error(err, "Failed to get action context")
		return err
	}
	org, repo := context.Repo()

	// Check if user is trusted first
	var user string
	if context.Event != nil {
		if comment, ok := context.Event["comment"].(map[string]any); ok {
			if userMap, ok := comment["user"].(map[string]any); ok {
				if login, ok := userMap["login"].(string); ok {
					user = login
				}
			}
		}
	}
	trusted, err := isTrusted(c.Context, gh, org, repo, user)
	if err != nil {
		m.log.Error(err, "Failed to check if user is trusted")
		return err
	}
	if !trusted {
		m.log.Info("User is not trusted")
		return nil
	}

	// Extract the new title and issue number
	var newTitle string
	var issueNumber float64
	if context.Event != nil {
		if comment, ok := context.Event["comment"].(map[string]any); ok {
			// Extract the comment body
			if body, ok := comment["body"].(string); ok {
				// Make sure they are requesting a re-title
				if !retitleRe.MatchString(body) {
					return nil
				}
				// Extract the new title
				newTitle = getNewTitle(body)
			}
		}
		// get the issue number
		if issue, ok := context.Event["issue"].(map[string]any); ok {
			if num, ok := issue["number"].(float64); ok {
				issueNumber = num
			}
		}
	}
	m.log.Info("New title", "title", newTitle)
	m.log.Info("Issue number", "number", issueNumber)

	// Update the title
	req := &github.IssueRequest{
		Title: &newTitle,
	}
	_, _, err = gh.Issues.Edit(c.Context, org, repo, int(issueNumber), req)
	if err != nil {
		m.log.Error(err, "Failed to update issue")
		return err
	}

	return nil
}

func getNewTitle(body string) string {
	matches := retitleRe.FindStringSubmatch(body)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

type owners struct {
	Reviewers []string `json:"reviewers"`
	Approvers []string `json:"approvers"`
}

func isTrusted(ctx context.Context, gh *github.Client, org, repo, user string) (bool, error) {
	// Read owners file
	ownersFile, _, _, err := gh.Repositories.GetContents(ctx, org, repo, "OWNERS", nil)
	if err != nil {
		return false, err
	}

	// Decode the file
	ownersData, err := ownersFile.GetContent()
	if err != nil {
		return false, err
	}

	// Check if the user is in the file
	var owners owners
	// Unmarshal the YAML string into the struct
	err = yaml.Unmarshal([]byte(ownersData), &owners)
	if err != nil {
		panic(err)
	}
	for _, reviewer := range owners.Reviewers {
		if reviewer == user {
			return true, nil
		}
	}
	for _, approver := range owners.Approvers {
		if approver == user {
			return true, nil
		}
	}

	return false, nil
}
