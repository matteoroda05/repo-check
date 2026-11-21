package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Relationship describes how the checkout's current commit compares with a target branch.
type Relationship string

const (
	Current  Relationship = "up to date"
	Ahead    Relationship = "ahead"
	Behind   Relationship = "behind"
	Diverged Relationship = "WARNING: diverged (ahead and behind)"
	Absent   Relationship = "branch absent on remote"
	Unknown  Relationship = "unavailable"
)

type Comparison struct {
	// Kind labels the target's role, such as matching branch, default branch, or upstream.
	Kind   string
	Target string
	// Tracking reports whether the target is the checkout's configured upstream.
	Tracking     bool
	Relationship Relationship
	Err          error
}

// RemoteStatus groups one remote's comparisons. Err records a discovery or fetch failure.
type RemoteStatus struct {
	Name        string
	Comparisons []Comparison
	Err         error
}

type Status struct {
	Head    string
	Note    string
	Remotes []RemoteStatus
	// Local holds upstream rows that cannot be grouped under a configured remote.
	Local []Comparison
	// Err records a checkout-level failure that prevented checking its remotes.
	Err error
}

// Check fetches the refs needed for the checkout's branch comparisons.
func (r *Repository) Check(ctx context.Context) Status {
	var status Status
	var branch plumbing.ReferenceName
	var tip *object.Commit
	// Resolve HEAD while allowing detached checkouts and branches with no commits.
	headRef, err := r.repo.Reference(plumbing.HEAD, false)
	if err != nil {
		status.Err = fmt.Errorf("read HEAD: %w", err)
		return status
	}
	if headRef.Type() == plumbing.SymbolicReference && headRef.Target().IsBranch() {
		branch = headRef.Target()
	}
	head, err := r.repo.Head()
	switch {
	case errors.Is(err, plumbing.ErrReferenceNotFound) && branch != "":
		status.Head = branch.Short()
		status.Note = "branch has no commits yet"
	case err != nil:
		status.Err = fmt.Errorf("resolve HEAD: %w", err)
		return status
	default:
		status.Head = branch.Short()
		if branch == "" {
			status.Head = "detached HEAD at " + head.Hash().String()[:7]
			status.Note = "no checked-out branch"
		}
		tip, err = r.repo.CommitObject(head.Hash())
		if err != nil {
			status.Err = fmt.Errorf("read current commit: %w", err)
			return status
		}
	}

	cfg, err := r.repo.ConfigScoped(config.GlobalScope)
	if err != nil {
		status.Err = fmt.Errorf("read Git configuration: %w", err)
		return status
	}
	shallow, err := r.repo.Storer.Shallow()
	if err != nil {
		status.Err = fmt.Errorf("read shallow history: %w", err)
		return status
	}
	// Handle missing upstreams and upstreams outside the remote loop.
	tracking := cfg.Branches[branch.Short()]
	if branch != "" && tip != nil {
		switch {
		case tracking == nil || tracking.Remote == "" || tracking.Merge == "":
			status.Note = "no configured upstream"
		case tracking.Remote == ".":
			status.Local = append(status.Local, r.compare(tip, len(shallow) > 0, tracking.Merge,
				Comparison{Kind: "Upstream", Target: tracking.Merge.Short(), Tracking: true}))
		case cfg.Remotes[tracking.Remote] == nil:
			status.Local = append(status.Local, Comparison{
				Kind: "Upstream", Target: tracking.Remote + "/" + tracking.Merge.Short(), Tracking: true,
				Relationship: Unknown, Err: errors.New("configured remote is missing"),
			})
		}
	}

	// Check remotes in display order with a separate timeout for each.
	names := make([]string, 0, len(cfg.Remotes))
	for name := range cfg.Remotes {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		remoteCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		result := r.checkRemote(remoteCtx, cfg.Remotes[name], branch, tracking, tip, len(shallow) > 0)
		cancel()
		status.Remotes = append(status.Remotes, result)
	}
	return status
}

func (r *Repository) compare(tip *object.Commit, shallow bool, ref plumbing.ReferenceName, result Comparison) Comparison {
	result.Relationship = Unknown
	if tip == nil {
		result.Err = errors.New("current branch has no commits")
		return result
	}
	target, err := r.repo.Reference(ref, true)
	if err != nil {
		result.Err = err
		return result
	}
	other, err := r.repo.CommitObject(target.Hash())
	if err != nil {
		result.Err = err
		return result
	}
	result.Relationship, result.Err = relationship(tip, other, shallow)
	return result
}

// relationship compares two commit tips by ancestry, from the perspective of tip.
// With shallow history, only identical tips can be classified.
func relationship(tip, other *object.Commit, shallow bool) (Relationship, error) {
	if tip.Hash == other.Hash {
		return Current, nil
	}
	if shallow {
		return Unknown, errors.New("history is shallow")
	}
	containsRemote, err := other.IsAncestor(tip)
	if err != nil {
		return Unknown, fmt.Errorf("read local ancestry: %w", err)
	}
	if containsRemote {
		return Ahead, nil
	}
	remoteContainsLocal, err := tip.IsAncestor(other)
	if err != nil {
		return Unknown, fmt.Errorf("read remote ancestry: %w", err)
	}
	if remoteContainsLocal {
		return Behind, nil
	}
	return Diverged, nil
}

// checkRemote reports the configured upstream separately when its name differs from the local branch.
func (r *Repository) checkRemote(ctx context.Context, remote *config.RemoteConfig, branch plumbing.ReferenceName,
	tracking *config.Branch, tip *object.Commit, shallow bool) RemoteStatus {
	result := RemoteStatus{Name: remote.Name}
	plans := []comparisonPlan{}
	if branch != "" {
		plans = append(plans, comparisonPlan{kind: "Matching branch", source: branch})
		if tracking != nil && remote.Name == tracking.Remote && tracking.Merge != "" {
			if tracking.Merge == branch {
				plans[0].tracking = true
			} else {
				plans = append(plans, comparisonPlan{kind: "Upstream", source: tracking.Merge, tracking: true})
			}
		}
	}

	url, advertised, err := advertise(ctx, r.Path, remote)
	if err != nil {
		result.Err = fmt.Errorf("read remote refs: %w", err)
	} else {
		plans = append(plans, comparisonPlan{kind: "Default branch", source: advertised.defaultBranch})
		destinations, fetchErr := r.fetch(ctx, remote, url, advertised, plans)
		if fetchErr != nil {
			result.Err = fmt.Errorf("fetch: %w", fetchErr)
		}
		// Use the advertised branch list so stale local refs cannot hide deletions.
		for _, plan := range plans {
			item := plan.comparison(remote.Name)
			switch {
			case plan.source == "":
				item.Err = errors.New("remote did not advertise a default branch")
			case !advertised.branches[plan.source]:
				item.Relationship = Absent
			case fetchErr == nil:
				item = r.compare(tip, shallow, destinations[plan.source], item)
			}
			result.Comparisons = append(result.Comparisons, item)
		}
		return result
	}
	// Keep the planned rows visible as unavailable if remote discovery fails.
	for _, plan := range append(plans, comparisonPlan{kind: "Default branch"}) {
		result.Comparisons = append(result.Comparisons, plan.comparison(remote.Name))
	}
	return result
}

type comparisonPlan struct {
	kind     string
	source   plumbing.ReferenceName
	tracking bool
}

func (p comparisonPlan) comparison(remote string) Comparison {
	target := remote + "/HEAD"
	if p.source != "" {
		target = remote + "/" + p.source.Short()
	}
	return Comparison{Kind: p.kind, Target: target, Tracking: p.tracking, Relationship: Unknown}
}
