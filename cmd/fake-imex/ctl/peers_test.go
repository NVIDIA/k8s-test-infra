// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"reflect"
	"testing"
)

func TestExpectedPeers(t *testing.T) {
	cases := []struct {
		name  string
		peers []string
		ownIP string
		want  []string
	}{
		{
			name:  "own IP missing from peers gets appended",
			peers: []string{"10.0.0.1", "10.0.0.2"},
			ownIP: "10.0.0.3",
			want:  []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"},
		},
		{
			name:  "own IP already in peers is not duplicated",
			peers: []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"},
			ownIP: "10.0.0.2",
			want:  []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"},
		},
		{
			name:  "empty ownIP returns peers unchanged",
			peers: []string{"10.0.0.1"},
			ownIP: "",
			want:  []string{"10.0.0.1"},
		},
		{
			name:  "single-node clique (empty peers) plus own IP yields own IP",
			peers: nil,
			ownIP: "10.0.0.5",
			want:  []string{"10.0.0.5"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExpectedPeers(tc.peers, tc.ownIP)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ExpectedPeers(%v, %q) = %v, want %v", tc.peers, tc.ownIP, got, tc.want)
			}
		})
	}
}
