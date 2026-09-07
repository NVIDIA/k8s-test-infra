# Enhancement Proposals

A Mokka Enhancement Proposal (MEP) is a design document for a substantial
change. The process is adapted from the Kubernetes
[KEP](https://github.com/kubernetes/enhancements/tree/master/keps) process.

Most changes do not need one. Bug fixes and routine features go straight to a
pull request.

## When to write one

Write a MEP if the change:

- reworks how existing components fit together;
- adds or removes a top-level component — a chart, a CLI, a service;
- alters a user-visible contract, such as configuration or Helm values;
- introduces a new dependency, platform target, or release artifact.

When in doubt, open a discussion first and a maintainer will say whether one is
needed.

## Writing it

1. **Copy the template.** `enhancements/template.md` → `enhancements/meps/NNNN-short-title/README.md`,
   where `NNNN` is the next unused four-digit number.
2. **Fill in Summary, Motivation and Goals first.** Enough for reviewers to
   argue about direction before you invest in Design Details. Getting told the
   whole approach is wrong is much cheaper at this point.
3. **Open a pull request** labelled `kind/mep`. Discussion happens on the PR.
4. **Iterate.** Expand Design Details, Risks and Alternatives as consensus
   forms.
5. **Merge** once maintainers approve.

A merged MEP is the record of intent, not a promise that the code matches it
yet. Implementation lands in later pull requests that reference it by number
(`MEP-NNNN`).

## Keeping it honest

A MEP describes a design at the time it was accepted. Some are fully built,
some partly, some have been overtaken by a different approach — and a reader
cannot tell which from the document alone.

If you implement, change or abandon part of a proposal, amend the MEP in the
same change. Superseded proposals stay in `meps/` rather than being deleted:
history is preserved, not rewritten.

## Reading the existing ones

| MEP | Proposes |
|---|---|
| [MEP-0001](https://github.com/NVIDIA/k8s-test-infra/tree/main/enhancements/meps/0001-mokka-control-plane) | A control plane owning cluster-wide simulated-GPU inventory |
| [MEP-0002](https://github.com/NVIDIA/k8s-test-infra/tree/main/enhancements/meps/0002-device-plugin-nri-composition) | How the NRI plugin composes with the NVIDIA device plugin |
| [MEP-0003](https://github.com/NVIDIA/k8s-test-infra/tree/main/enhancements/meps/0003-node-agent) | The node daemon: one reconciler replacing a dozen CLIs |

MEP-0003 also carries the project's most complete record of which contract
surfaces are and are not simulated.

## Related

| To read about | See |
|---|---|
| Submitting the implementation | [Pull Requests](pull-requests.md) |
| How the components fit together today | [Architecture](../architecture.md) |
