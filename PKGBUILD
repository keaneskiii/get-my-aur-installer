# Maintainer: keaneskiii <keaneskiii at github>
#
# build and install with any of:
#   yay -S gmai
#   paru -S gmai
#   makepkg -si        (this is the pacman path; it ends in `sudo pacman -U`)
#
pkgname=gmai
pkgver=1.0.0
pkgrel=1
pkgdesc="terminal ui to search, install, update and remove arch repo and aur packages"
arch=('x86_64')
url="https://github.com/keaneskiii/get-my-aur-installer"
license=('MIT')  # TODO: add a LICENSE file to the repo and confirm this matches
depends=('glibc')
makedepends=('git' 'go')
provides=('gmai')
options=('!lto' '!debug')
source=("${pkgname}-${pkgver}.git::${url}.git")
sha256sums=('SKIP')

# find the source tree: a git source lands in $srcdir as ${pkgname}-${pkgver}
# (or ${pkgname} once pkgver() has run). fall back to a search so the build
# works no matter which name makepkg picked.
srcdir_of() {
  local d
  for d in "${srcdir}/${pkgname}" "${srcdir}/${pkgname}-${pkgver}" "${srcdir}"/*/; do
    if [ -f "${d}go.mod" ]; then
      cd "${d}"
      return 0
    fi
  done
  echo "could not find the go source tree under ${srcdir}" >&2
  return 1
}

pkgver() {
  # the git source is cloned into $srcdir as ${pkgname}-${pkgver}
  cd "${srcdir}/${pkgname}-${pkgver}" 2>/dev/null ||
    cd "${srcdir}/${pkgname}" ||
    cd "${srcdir}"/*/
  # works with a plain tag (v1.0.0) or falls back to a commit count
  git describe --long --tags --abbrev=8 2>/dev/null |
    sed 's/^v//;s/-/./g' ||
    printf "r%s.g%s" "$(git rev-list --count HEAD)" \
      "$(git rev-parse --short HEAD)"
}

build() {
  srcdir_of
  export CGO_CPPFLAGS="${CPPFLAGS}"
  export CGO_CFLAGS="${CFLAGS}"
  export CGO_CXXFLAGS="${CXXFLAGS}"
  export CGO_LDFLAGS="${LDFLAGS}"
  export GOFLAGS="-buildmode=pie -trimpath"
  go build -ldflags "-s -w -X main.version=${pkgver}" -o "${pkgname}" .
}

check() {
  srcdir_of
  go test ./...
}

package() {
  srcdir_of
  install -Dm755 "${pkgname}" "${pkgdir}/usr/bin/${pkgname}"
  install -Dm644 README.md "${pkgdir}/usr/share/doc/${pkgname}/README.md"
}
