package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
)

type advertisement struct {
	branches      map[plumbing.ReferenceName]bool
	defaultBranch plumbing.ReferenceName
}

// Use the remote's actual symbolic HEAD. Remote.ListContext can infer a branch
// from a HEAD hash when the server omits the symbolic reference.
func advertise(ctx context.Context, path string, remote *config.RemoteConfig) (string, advertisement, error) {
	var result advertisement
	if err := remote.Validate(); err != nil {
		return "", result, err
	}
	url := remote.URLs[0]
	endpoint, err := transport.NewEndpoint(url)
	if err != nil {
		return "", result, err
	}
	if endpoint.Protocol == "file" && !filepath.IsAbs(url) && !strings.HasPrefix(url, "file:") {
		url = filepath.Join(path, url)
		endpoint.Path = url
	}
	transportClient, err := client.NewClient(endpoint)
	if err != nil {
		return "", result, err
	}
	// With no explicit credentials, go-git's SSH transport uses the SSH agent.
	session, err := transportClient.NewUploadPackSession(endpoint, nil)
	if err != nil {
		return "", result, err
	}
	defer session.Close()

	refs, err := session.AdvertisedReferencesContext(ctx)
	if errors.Is(err, transport.ErrEmptyRemoteRepository) {
		return url, result, nil
	}
	if err != nil {
		return "", result, err
	}
	result.branches = make(map[plumbing.ReferenceName]bool)
	for name := range refs.References {
		ref := plumbing.ReferenceName(name)
		if ref.IsBranch() {
			result.branches[ref] = true
		}
	}
	for _, symref := range refs.Capabilities.Get(capability.SymRef) {
		if branch, found := strings.CutPrefix(symref, "HEAD:"); found {
			ref := plumbing.ReferenceName(branch)
			if ref.IsBranch() && ref.Validate() == nil {
				result.defaultBranch = ref
			}
			break
		}
	}
	return url, result, nil
}

func (r *Repository) fetch(ctx context.Context, remote *config.RemoteConfig, url string,
	advertised advertisement, plans []comparisonPlan) (map[plumbing.ReferenceName]plumbing.ReferenceName, error) {
	destinations := make(map[plumbing.ReferenceName]plumbing.ReferenceName)
	used := make(map[plumbing.ReferenceName]bool)
	var specs []config.RefSpec
	for _, plan := range plans {
		src := plan.source
		if !advertised.branches[src] || destinations[src] != "" {
			continue
		}
		dst, force := r.fetchDestination(remote, src)
		if used[dst] {
			return nil, fmt.Errorf("multiple remote branches map to %s", dst)
		}
		if err := dst.Validate(); err != nil {
			return nil, err
		}
		spec := src.String() + ":" + dst.String()
		if force {
			spec = "+" + spec
		}
		specs = append(specs, config.RefSpec(spec))
		destinations[src] = dst
		used[dst] = true
	}
	if len(specs) == 0 {
		return destinations, nil
	}
	err := git.NewRemote(r.repo.Storer, remote).FetchContext(ctx, &git.FetchOptions{
		RemoteURL: url,
		RefSpecs:  specs,
		Tags:      git.NoTags,
	})
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		err = nil
	}
	return destinations, err
}

func (r *Repository) fetchDestination(remote *config.RemoteConfig, src plumbing.ReferenceName) (plumbing.ReferenceName, bool) {
	for _, spec := range remote.Fetch {
		if !spec.Match(src) {
			continue
		}
		dst := spec.Dst(src)
		if !strings.HasPrefix(dst.String(), "refs/remotes/"+remote.Name+"/") ||
			dst == plumbing.NewRemoteHEADReferenceName(remote.Name) {
			continue
		}
		if ref, err := r.repo.Reference(dst, false); err == nil && ref.Type() == plumbing.SymbolicReference {
			continue
		}
		return dst, spec.IsForceUpdate()
	}
	// A single-branch clone may have no tracking ref for the remote default.
	// Keep that tip under the tool's own namespace and preserve the Git config.
	return plumbing.ReferenceName("refs/repo-check/" + remote.Name + "/heads/" + src.Short()), true
}
