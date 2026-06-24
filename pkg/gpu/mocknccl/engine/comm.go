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

package engine

import "time"

// Comm is a resolved communicator: membership + cost model + sleep cap.
type Comm struct {
	Rank      int
	WorldSize int
	InterNode bool
	Model     Model
	MaxSleep  time.Duration
}

// RunCollective models one collective call: it computes the modeled duration,
// sleeps for min(modeled, MaxSleep) on the host, and returns the MODELED
// duration (callers time it via CUDA events, which capture the real sleep; the
// returned value is for engine-side assertions/debug). Reported time is the
// measured sleep, accurate while modeled <= cap. A MaxSleep <= 0 disables the
// cap, so the host sleeps for the full modeled duration with no upper bound.
func (c *Comm) RunCollective(op Collective, msgBytes int64) time.Duration {
	modeled := c.Model.OpTime(op, msgBytes, c.WorldSize, c.InterNode)
	sleep := modeled
	if c.MaxSleep > 0 && sleep > c.MaxSleep {
		sleep = c.MaxSleep
	}
	if sleep > 0 {
		time.Sleep(sleep)
	}
	return modeled
}
