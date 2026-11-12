// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import "nvpair-shared/noderec"

// localServedLoaded is the loaded set directory residency compares a request's
// model with. A loaded-only engine (vLLM: the node has a downloadedByEngine
// entry for it) names its loadedByEngine models by --model root (NVIDIA #181),
// but a request names the served model, which is what its modelsByEngine
// advertises; a stopped one advertises nothing, so nothing is loaded. Any
// other engine keeps loadedByEngine.
func localServedLoaded(n noderec.DirectoryNode, engine string, loaded []string) []string {
	if _, loadedOnly := n.DownloadedByEngine[engine]; loadedOnly {
		return n.ModelsByEngine[engine]
	}
	return loaded
}
