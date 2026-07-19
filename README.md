<p align="center">
  <img src="https://img.shields.io/badge/status-pre--release-orange" alt="pre-release" />
  <a href="https://github.com/thebolaji/infraward/actions/workflows/ci.yml"><img src="https://github.com/thebolaji/infraward/actions/workflows/ci.yml/badge.svg" alt="CI status" /></a>
  <a href="https://goreportcard.com/report/github.com/thebolaji/infraward"><img src="https://goreportcard.com/badge/github.com/thebolaji/infraward" alt="Go Report Card" /></a>
  <img src="https://img.shields.io/github/go-mod/go-version/thebolaji/infraward" alt="Go version" />
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License: Apache-2.0" /></a>
</p>

<h1 align="center">InfraWard</h1>

<p align="center">
  Reconcile Terraform state against what actually exists in your AWS account.
</p>

<p align="center">
  <code>terraform plan</code> tells you what <em>would</em> change in state it already knows about.<br/>
  InfraWard tells you what exists in AWS that <b>no state manages at all.</b>
</p>

---

## Table of contents

- [Why InfraWard](#why-infraward)
- [Install](#install)
- [Quick start](#quick-start)
- [How it works](#how-it-works)
- [Flags](#flags)
- [Suppressing noise](#suppressing-noise)
- [IAM permissions](#iam-permissions)
- [How it compares](#how-it-compares)
- [Project status](#project-status)
- [Non-goals](#non-goals)
- [Contributing](#contributing)
- [License](#license)

## Why InfraWard

Every team with an AWS account accumulates resources Terraform doesn't know
about: something clicked into existence in the console, a resource orphaned
by a half-finished migration, infrastructure a departed engineer set up by
hand. `terraform plan` can't find any of it — it only ever compares state
against itself.

InfraWard reads AWS directly through the [AWS Cloud Control
API](https://docs.aws.amazon.com/cloudcontrolapi/latest/userguide/what-is-cloudcontrolapi.html)
and diffs it against your Terraform state, so resource coverage grows by
editing a data table (see [`internal/mapping`](internal/mapping/mapping.go)),
not by shipping a hand-written reader for every AWS service.

## Install

> **No tagged release yet.** Build from source until the first `v*` tag ships.

```sh
git clone https://github.com/thebolaji/infraward
cd infraward
go build -o infraward ./cmd/infraward
```

Once a release is tagged, these will also work (already wired up in
[`.goreleaser.yml`](.goreleaser.yml) and
[`.github/workflows/release.yml`](.github/workflows/release.yml)):

```sh
# via Homebrew
brew install thebolaji/infraward/infraward

# via go install
go install github.com/thebolaji/infraward/cmd/infraward@latest
```

## Quick start

```sh
infraward drift --state s3://my-tf-states/prod/ --region us-east-1
```

```
TYPE           ID                        REGION     STATUS
aws_instance   i-0456bf8df48b43b2f       us-east-1  unmanaged
aws_vpc        vpc-0f216e50261fc7fbd     us-east-1  unmanaged
aws_s3_bucket  my-old-test-bucket                   missing

214 resources in AWS, 167 managed (78%), 41 unmanaged, 6 missing.
```

| Status | Meaning |
|---|---|
| **unmanaged** | Exists in AWS. No state manages it. |
| **missing** | State says it exists. It doesn't anymore. |

Wire it into CI with no extra scripting — the exit code does the work:

| Exit code | Meaning |
|:---:|---|
| `0` | Clean — nothing found |
| `1` | Findings — unmanaged or missing resources exist |
| `2` | Error |

Run it again later with `--since-last` and it only reports what's newly
unmanaged or newly missing since the previous scan — every scan is recorded
locally in `.infraward/infraward.db`.

## How it works

1. **Read state** — parses your Terraform state (local file, glob, or
   `s3://bucket/prefix`, which discovers every `.tfstate` key under it).
2. **Read AWS** — concurrently lists live resources via Cloud Control's
   `ListResources` across your target regions, plus a hand-written Route53
   reader for DNS records (the one type Cloud Control doesn't cover).
3. **Diff** — matches live resources against state by type and
   identifier (`id` or `arn`), and reports what's on only one side.
4. **Suppress** — collapses known-noise findings per `.infraward.yml`.
5. **Record** — every scan is saved to a local SQLite baseline for `--since-last`.

## Flags

| Flag | Description |
|---|---|
| `--state` | Terraform state source: local path, glob, or `s3://bucket/prefix`. Repeatable. |
| `--region` | AWS region to scan. Repeatable; defaults to your AWS config's default region. |
| `--filter` | Limit scanned Cloud Control types, e.g. `--filter 'AWS::EC2::*'`. Repeatable. |
| `--output` | `table` (default), `json`, or `github` (step-summary friendly). |
| `--show-ignored` | Include suppressed findings instead of collapsing them. |
| `--since-last` | Report only what changed since the previous scan. |

## Suppressing noise

Not everything InfraWard finds is a problem — a hand-created test bucket,
say. Put suppression rules in `.infraward.yml`, in the directory you run
`infraward` from:

```yaml
suppress:
  - type: aws_iam_policy                          # ignore every finding of this Terraform type
  - id: i-0123456789abcdef0                        # ignore one specific resource
  - id_pattern: "arn:aws:elasticloadbalancing:*"    # ignore by glob ("*" and "?")
  - type: aws_instance                              # tag rules must be paired with
    tag:                                             # type, id, or id_pattern — see below
      managed-by: console
```

Suppressed findings still count toward the coverage line — they're
collapsed from the list, not deleted. Reveal them, annotated with the rule
that matched, with:

```sh
infraward drift --state ./prod.tfstate --show-ignored
```

> [!NOTE]
> **`tag:` rules must always be paired with `type`, `id`, or `id_pattern`.**
> Checking a resource's tags costs one extra AWS API call per resource
> (`ListResources` only returns IDs), so InfraWard only fetches tags for
> resources a rule could plausibly match. A `tag:` rule with nothing to
> narrow it — "ignore anything, anywhere, tagged X" — would have to check
> every unmanaged resource's tags to know for sure, which reliably triggers
> AWS throttling on any account with a meaningful number of unmanaged
> resources. A config like that fails to load with an error telling you to
> add a `type`.

## IAM permissions

InfraWard never writes to AWS — it only calls list/read APIs. This is
everything `drift` currently uses:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "CloudControlDiscovery",
      "Effect": "Allow",
      "Action": ["cloudcontrol:ListResources", "cloudcontrol:GetResource"],
      "Resource": "*"
    },
    {
      "Sid": "Route53RecordSets",
      "Effect": "Allow",
      "Action": ["route53:ListHostedZones", "route53:ListResourceRecordSets"],
      "Resource": "*"
    },
    {
      "Sid": "StateFileAccess",
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:ListBucket"],
      "Resource": [
        "arn:aws:s3:::your-tfstate-bucket",
        "arn:aws:s3:::your-tfstate-bucket/*"
      ]
    }
  ]
}
```

Scope `StateFileAccess` down to your actual state bucket(s). The AWS-managed
`ReadOnlyAccess` policy also works if you'd rather not maintain a custom one.

## How it compares

| | InfraWard | driftctl | `terraform plan` | Firefly / env0 / HCP Terraform |
|---|:---:|:---:|:---:|:---:|
| Finds unmanaged AWS resources | ✅ | ✅ (unmaintained) | ❌ | ✅ |
| Actively maintained | ✅ | ❌ | — | ✅ |
| Cost | Free, open source | Free, open source | Free | Paid platform |
| Distribution | Single binary | Single binary | Part of Terraform | SaaS |
| Coverage strategy | AWS Cloud Control API (data-driven) | Hand-written reader per type | — | Proprietary |
| Cross-state index / duplicate detection | Roadmapped (`index`) | ❌ | ❌ | Some |
| Generates Terraform for adoption | Roadmapped (`adopt`) | Limited | `-generate-config-out` (manual cleanup) | Some |

## Project status

Only `infraward drift` exists today, and it's pre-release (no tagged
version yet). Everything below is tracked, roughly in priority order:

- [x] `drift`: unmanaged + missing detection
- [x] Local and S3 Terraform state
- [x] Suppressions (`.infraward.yml`): type, ID, ID pattern, tag
- [x] `--since-last` baseline mode
- [x] `table` / `json` / `github` output, CI-ready exit codes
- [x] Offline unit test suite
- [ ] Integration tests against real AWS API responses (LocalStack or recorded fixtures)
- [ ] `infraward adopt` — generate Terraform for unmanaged resources
- [ ] `infraward index` — cross-state reverse lookup, duplicate/orphan detection
- [ ] Attribute-level drift on already-managed resources

## Non-goals

- No `apply`, no writes to AWS, ever.
- No policy/compliance linting — use Checkov, tfsec, or OPA.
- No cost estimation — use Infracost.
- No CI runner or PR automation platform — use Atlantis or Digger. InfraWard
  is a CLI those tools can call, not a replacement for them.
- No multi-cloud support.

## Contributing

The highest-value contribution is extending
[`internal/mapping`](internal/mapping/mapping.go): a Terraform resource type
mapped to its AWS Cloud Control type name is a data table entry, not new Go
code. **Verify coverage against a real account before adding an entry** —
several entries in the current table needed correction after testing live;
see the comments above `Table` in that file for specifics.

See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for the branching strategy,
commit conventions, and local checks to run before opening a PR.

## License

[Apache-2.0](./LICENSE). No telemetry, ever.
