# Contributing to an OCM SIG

Welcome, and thanks for your interest in joining one of the Open Component Model (OCM) Special Interest Groups (SIG).
This document is the front door for participating in a SIG. It points you at the channels, ceremonies, and paths that
help you navigate the processes around this SIG.

This guide covers participation in the SIG itself. For contributing **code** to OCM repositories see the [Open Component Model Contribution Guidelines](https://github.com/open-component-model/.github/blob/main/CONTRIBUTING.md).
For the formal governance rules, see the [SIG Handbook](./SIG-Handbook.md).

## Contents

- [Before You Start](#before-you-start)
- [Ways to Participate](#ways-to-participate)
- [Communication](#communication)
- [Meetings and Ceremonies](#meetings-and-ceremonies)
- [Becoming a Maintainer or Voting Member](#becoming-a-maintainer-or-voting-member)
- [Proposing Changes to a SIG](#proposing-changes-to-a-sig)
  - [Charter or scope change](#charter-or-scope-change)
  - [New SIG](#new-sig)
  - [Broad technical decisions](#broad-technical-decisions)
- [Code of Conduct](#code-of-conduct)
- [Need Help?](#need-help)

## Before You Start

1. Read the [SIG Handbook](./SIG-Handbook.md) for governance and lifecycle rules.
2. Browse [`sigs.yaml`](./sigs.yaml) and pick the SIG whose scope matches your interest. Each SIG has its own folder with a charter and meeting notes.
3. Skim the relevant SIG charter so you know what is in scope and what is not.
4. Join the communication channels listed for that SIG (see [Communication](#communication)).
5. Read the [OCM Code of Conduct](https://github.com/open-component-model/.github/blob/main/CODE_OF_CONDUCT.md). It applies to every interaction.

## Ways to Participate

There is no single ladder. Pick the entry points that fit how you want to contribute.

- **Attend a community call.** Show up, listen, ask questions. Recordings and the schedule live in [`docs/community/README.md`](../README.md).
- **Hang out in the chat.** Lurking is fine. Answer a question if you can.
- **Triage issues.** Reproduce bugs, label, ask for missing details, close stale ones.
- **Take a `good first issue`.** The fastest way to learn the code and build trust.
- **Review pull requests.** Even non-binding reviews are useful and help you learn the codebase.
- **Improve documentation.** READMEs, examples, troubleshooting guides, charter polish.
- **Bring a use case or demo.** Present at a community call. Real adoption stories shape the roadmap.
- **Adopt a subproject or area of ownership.** Once you are a known contributor, take responsibility for a slice of the SIG's scope.
- **Propose a new initiative.** Working group, ADR, charter amendment. Open a PR or raise it on the agenda.

## Communication

Each SIG lists its channels in [`sigs.yaml`](./sigs.yaml) and in its charter. The OCM-wide channels are:

| Channel        | Where                                                                                                       |
|----------------|-------------------------------------------------------------------------------------------------------------|
| Zulip          | [neonephos-ocm-support](https://linuxfoundation.zulipchat.com/#narrow/channel/532975-neonephos-ocm-support) |
| Mailing list   | `open-component-model-sig-<sig-name>@lists.neonephos.org`                                                   |
| GitHub         | [open-component-model](https://github.com/open-component-model)                                             |
| Community page | [ocm.software/community](https://ocm.software/community)                                                    |

For SIG-specific channels (per-SIG mailing list, dedicated meetings), see the SIG's own folder under [`docs/community/SIGs/`](.).

## Meetings and Ceremonies

OCM runs a monthly community call that all SIGs participate in. Cadence and links are listed per SIG.

TODO: Link to OCM community Call.

| Ceremony             | Cadence               | Link                                              | Notes                                                                             |
|----------------------|-----------------------|---------------------------------------------------|-----------------------------------------------------------------------------------|
| OCM Community Call   | Monthly               | [Engagement page](https://ocm.software/community) | Shared across all SIGs. Recordings in [`docs/community/README.md`](../README.md). |
| SIG-specific meeting | _see SIG charter_     | _see SIG charter_                                 | Optional, defined per SIG.                                                        |
| TSC meeting          | _see steering folder_ | [TSC notes](../../steering/meeting-notes)         | Charter approvals and major decisions.                                            |

Meeting notes go into the SIG's `meeting-notes/` subfolder. Public, dated, one file per meeting.

## Becoming a Maintainer or Voting Member

Voting rights and maintainer status are earned through sustained, visible work in the SIG:

1. Contribute regularly: PRs, reviews, triage, meeting attendance.
2. An existing voting member of the SIG nominates you.
3. The nomination is confirmed by a majority vote at a public SIG meeting, with quorum.
4. You are added to the SIG's leadership listing and, if applicable, to `CODEOWNERS`.

Voting rights may lapse after extended inactivity. The full rules, including quorum, supermajority thresholds, and removal, are in the [SIG Handbook](./SIG-Handbook.md#24-decision-making--tsc-approval).

## Proposing Changes to a SIG

### Charter or scope change

Open a PR against the SIG's charter. Add the change to the next TSC agenda.
Major changes require a two-thirds supermajority of voting members and TSC approval.

### New SIG

Follow [Section 2.3 of the Handbook](./SIG-Handbook.md#23-sig-creation--charter-requirements).

### Broad technical decisions

For larger changes, please open an [ADR](../../adr) first.

## Code of Conduct

All participation is governed by the [OCM Code of Conduct](https://github.com/open-component-model/.github/blob/main/CODE_OF_CONDUCT.md). Report concerns through the channels listed there.

## Need Help?

Ask in [Zulip](https://linuxfoundation.zulipchat.com/#narrow/channel/532975-neonephos-ocm-support), on the SIG mailing list, or at the next community call. The Chair and Tech Lead listed in each
SIG's charter are the right first contacts.
