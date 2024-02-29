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
	"regexp"
	"strings"

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

	context, err := action.Context()
	if err != nil {
		m.log.Error(err, "Failed to get action context")
		return err
	}

	var newTitle string
	var issueNumber int
	if context.Event != nil {
		if comment, ok := context.Event["comment"].(map[string]any); ok {
			if body, ok := comment["body"].(string); ok {
				// Make sure they are requesting a re-title
				if !retitleRe.MatchString(body) {
					return nil
				}
				// Extract the new title
				newTitle = getNewTitle(body)
			}
		}
		if issue, ok := context.Event["issue"].(map[string]any); ok {
			if num, ok := issue["number"].(int); ok {
				issueNumber = num
			}
		}
	}
	m.log.Info("New title", "title", newTitle)

	org, repo := context.Repo()
	ghToken := action.Getenv("GITHUB_TOKEN")
	gh := github.NewClient(nil).WithAuthToken(ghToken)

	// Update the title
	req := &github.IssueRequest{
		Title: &newTitle,
	}
	_, _, err = gh.Issues.Edit(c.Context, org, repo, issueNumber, req)
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
