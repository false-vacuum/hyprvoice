# Release Process

How to cut a release of this fork and publish it to the AUR.

## Version scheme

Tags are `vMAJOR.MINOR.PATCH`, continuing upstream's numbering (the last upstream tag is `v1.0.2`).

- **Major**: breaking changes
- **Minor**: new features, backwards compatible
- **Patch**: bug fixes, small improvements

The `hyprvoice-git` package does not need a tag to build. Its `pkgver()` derives a version from `git describe`, so `v1.0.2` plus 12 commits becomes `1.0.2.r12.g7eae5f9`, which orders above `1.0.2`. Tags only matter for readable versions and GitHub releases.

## Cut a release

```bash
git checkout main
git pull origin main
go build ./... && go vet ./... && go test ./...
python3 overlay/test_state.py

git tag v1.1.0
git push origin main
git push origin v1.1.0
```

`.github/workflows/release.yml` builds `hyprvoice-linux-x86_64` on a `v*` tag, runs the tests, and attaches the binary and its checksum to a GitHub release. The workflow is inherited from upstream and publishes to this fork's repo.

## Publish to the AUR

The AUR package is `hyprvoice-git`, whose repository is separate from this one. Only `PKGBUILD` and `.SRCINFO` live there.

Before the first publish, `packaging/hyprvoice-git/PKGBUILD` needs its `source=` switched from the local `file://` path to the public remote:

```bash
source=("hyprvoice::git+https://github.com/false-vacuum/hyprvoice.git")
```

A VCS package builds whatever is on `main`, so push first, then:

```bash
git clone ssh://aur@aur.archlinux.org/hyprvoice-git.git
cd hyprvoice-git
cp ../hyprvoice/packaging/hyprvoice-git/PKGBUILD .
makepkg --printsrcinfo > .SRCINFO
makepkg -si                      # confirm it builds and installs clean
git add PKGBUILD .SRCINFO
git commit -m "upgpkg: hyprvoice-git 1.1.0"
git push
```

Publishing needs an SSH key registered on your AUR account. Regenerate `.SRCINFO` on every change to `PKGBUILD`; the AUR rejects pushes where the two disagree.

`hyprvoice-bin` on the AUR is upstream's, pinned to the archived v1.0.2. It is not ours and is not updated by this process.

## Checklist

- [ ] `go build ./... && go vet ./... && go test ./...`
- [ ] `python3 overlay/test_state.py`
- [ ] `cd packaging/hyprvoice-git && makepkg -si` succeeds from a clean tree
- [ ] `namcap` clean on the built package
- [ ] README reflects anything new
- [ ] Tag pushed, GitHub Actions green, binary attached to the release
- [ ] AUR updated and the package page shows the new version

## Troubleshooting

**`not a clone of ...`**: makepkg caches the source clone next to the PKGBUILD and refuses to reuse it when `source=` changes. `rm -rf packaging/hyprvoice-git/hyprvoice`.

**`pkgver()` fails**: the build needs tags. A tarball download has no `.git`, and a shallow clone may have no tags reachable from `HEAD`.

**Build fails in a clean chroot**: check `makedepends`. Use `extra-x86_64-build` to reproduce what an AUR user's clean environment sees.
