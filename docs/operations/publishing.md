# Publishing Guide (v0.6)

## Goal

Public publishing is treated as a filtered export problem, not as “push the current development worktree directly”.

That distinction exists because the development repository may contain:

- local runtime state under `data/`
- generated build artifacts under `build/`
- local-only optional archive corpora under `data/bootstrap/datasets/archives/`
- editor state, caches, logs, and other machine-local files

## Public source policy

Tracked and publish-safe by default:

- source code
- configs and examples
- docs
- tests
- deploy assets
- `dataset/raw/structured/` seed knowledge-base files
- `dataset/raw/archives/manifest.json` and `README.md`

Local-only and intentionally excluded from the public tree:

- `data/`
- `build/`
- `data/bootstrap/datasets/archives/`
- controller/runtime databases and caches
- RAG indexes and extraction caches

## Large-file strategy

The public repository ships a small seed dataset and a manifest for larger optional archives.

Why:

- GitHub warns on files above normal usage thresholds
- clone size matters for contributors and CI
- most contributors do not need the heavyweight local corpora to build or test the code

Alternatives considered:

- Git LFS: workable, but not the default because it adds extra tooling/bandwidth requirements to every clone path
- release assets only: useful later, but weaker for iterative local dataset work
- direct history rewrite of the dev repo: riskier than keeping a clean public export path

## Commands

Prepare and audit a publish-safe tree:

```bash
make publish-check
```

Prepare a local mirror repo and commit without pushing:

```bash
./update_github.sh --no-push
```

Create a versioned snapshot branch in the mirror repo without pushing:

```bash
./update_sync_and_push.sh --no-push
```

## Mirror repository workflow

Typical usage:

```bash
export SRE_PUBLISH_TARGET_DIR=/path/to/public-mirror-repo
export SRE_PUBLISH_REMOTE=origin
export SRE_PUBLISH_BRANCH=main
# optional when the target repo is new
export SRE_PUBLISH_REMOTE_URL=git@github.com:you/ai_sre_agent.git
./update_github.sh
```

Mechanically the publish helper does this:

1. verify the source repo is a git worktree
2. export only tracked + non-ignored source files into the target directory
3. audit the target tree for oversized files and obvious secret-like filenames
4. commit in the target mirror repo
5. push only if a remote is configured and push was not disabled

## Optional archive corpora

Import local archive corpora into the bootstrap store with:

```bash
make bootstrap-datasets SRC=/path/to/archive-dir
```

That populates:

```text
data/bootstrap/datasets/archives/
```

and refreshes:

```text
dataset/raw/archives/manifest.json
```
