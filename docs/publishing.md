# Publishing

The main Go module is `github.com/daviddwlee84/lazypg`; the executable package is
`./cmd/lazypg`. MIT covers project code. The development skill keeps its own notice.

CI runs the minimum Go version from go.mod, vet, race tests, four native OS/arch
runners, Linux Docker integration, and source/module/binary distribution checks.
Release assets use GoReleaser 2.18.2; see its [archive documentation](https://goreleaser.com/customization/package/archives/).
Workflow tokens are read-only except the release job's contents permission; see
[GitHub permissions](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#permissions).

After main CI passes on the exact release candidate, create a new immutable stable
`vMAJOR.MINOR.PATCH` tag. Never move published tags or replace public assets.
The tag workflow repeats CI, builds four binaries plus a source archive, verifies
checksums, archive paths, executable architecture, source builds and native smoke,
then uploads and verifies a complete draft before publication. A failed draft is
resumable only when its existing assets match. If artifacts differ, investigate
instead of overwriting. Release notes must agree with the changelog.

The scripts in scripts/release.py and scripts/check-distribution.py and their
contract tests are adapted from daviddwlee84/lazypkg under MIT.

The personal `daviddwlee84/homebrew-tap` owns formula rendering and the only writer.
Register lazypg in its tools.json (four archives, checksums.txt, bundled bash/zsh
completions); do not add a tap token to this repository. After a public release:

```sh
gh workflow run sync.yml --repo daviddwlee84/homebrew-tap -f tool=lazypg
```

The tap validates public assets, installs and tests on an isolated Linux runner,
then commits a generated formula and receipt. Its hourly sync handles subsequent
versions. Public release verification must also exercise fixed-tag and latest Go
installs from outside the checkout and Homebrew installation on a clean runner.

The original private repository keeps its complete history. Public history is
prepared in an independent clone by excluding development conversations and
private plan directories, then scanned again. Push only explicitly reviewed main
and release tags, never a mirror or all local refs. The private original must not
push unsanitized history to the public repository.
