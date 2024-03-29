// Copyright 2013 The go-github AUTHORS. All rights reserved.
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"context"
	"fmt"
)

// ListFollowers lists the followers for a user. Passing the empty string will
// fetch followers for the authenticated user.
//
// GitHub API docs: https://docs.github.com/rest/users/followers#list-followers-of-a-user
// GitHub API docs: https://docs.github.com/rest/users/followers#list-followers-of-the-authenticated-user
//
//meta:operation GET /user/followers
//meta:operation GET /users/{username}/followers
func (s *UsersService) ListFollowers(ctx context.Context, user string, opts *ListOptions) ([]*User, *Response, error) {
	var u string
	if user != "" {
		u = fmt.Sprintf("users/%v/followers", user)
	} else {
		u = "user/followers"
	}
	u, err := addOptions(u, opts)
	if err != nil {
		return nil, nil, err
	}

	req, err := s.client.NewRequest("GET", u, nil)
	if err != nil {
		return nil, nil, err
	}

	var users []*User
	resp, err := s.client.Do(ctx, req, &users)
	if err != nil {
		return nil, resp, err
	}

	return users, resp, nil
}

// ListFollowing lists the people that a user is following. Passing the empty
// string will list people the authenticated user is following.
//
// GitHub API docs: https://docs.github.com/rest/users/followers#list-the-people-a-user-follows
// GitHub API docs: https://docs.github.com/rest/users/followers#list-the-people-the-authenticated-user-follows
//
//meta:operation GET /user/following
//meta:operation GET /users/{username}/following
func (s *UsersService) ListFollowing(ctx context.Context, user string, opts *ListOptions) ([]*User, *Response, error) {
	var u string
	if user != "" {
		u = fmt.Sprintf("users/%v/following", user)
	} else {
		u = "user/following"
	}
	u, err := addOptions(u, opts)
	if err != nil {
		return nil, nil, err
	}

	req, err := s.client.NewRequest("GET", u, nil)
	if err != nil {
		return nil, nil, err
	}

	var users []*User
	resp, err := s.client.Do(ctx, req, &users)
	if err != nil {
		return nil, resp, err
	}

	return users, resp, nil
}

// IsFollowing checks if "user" is following "target". Passing the empty
// string for "user" will check if the authenticated user is following "target".
//
// GitHub API docs: https://docs.github.com/rest/users/followers#check-if-a-person-is-followed-by-the-authenticated-user
// GitHub API docs: https://docs.github.com/rest/users/followers#check-if-a-user-follows-another-user
//
//meta:operation GET /user/following/{username}
//meta:operation GET /users/{username}/following/{target_user}
func (s *UsersService) IsFollowing(ctx context.Context, user, target string) (bool, *Response, error) {
	var u string
	if user != "" {
		u = fmt.Sprintf("users/%v/following/%v", user, target)
	} else {
		u = fmt.Sprintf("user/following/%v", target)
	}

	req, err := s.client.NewRequest("GET", u, nil)
	if err != nil {
		return false, nil, err
	}

	resp, err := s.client.Do(ctx, req, nil)
	following, err := parseBoolResponse(err)
	return following, resp, err
}

// Follow will cause the authenticated user to follow the specified user.
//
// GitHub API docs: https://docs.github.com/rest/users/followers#follow-a-user
//
//meta:operation PUT /user/following/{username}
func (s *UsersService) Follow(ctx context.Context, user string) (*Response, error) {
	u := fmt.Sprintf("user/following/%v", user)
	req, err := s.client.NewRequest("PUT", u, nil)
	if err != nil {
		return nil, err
	}

	return s.client.Do(ctx, req, nil)
}

// Unfollow will cause the authenticated user to unfollow the specified user.
//
// GitHub API docs: https://docs.github.com/rest/users/followers#unfollow-a-user
//
//meta:operation DELETE /user/following/{username}
func (s *UsersService) Unfollow(ctx context.Context, user string) (*Response, error) {
	u := fmt.Sprintf("user/following/%v", user)
	req, err := s.client.NewRequest("DELETE", u, nil)
	if err != nil {
		return nil, err
	}

	return s.client.Do(ctx, req, nil)
}
