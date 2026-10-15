# gmai (get my aur installer)

**work in progress** — this is an early build. things will change, some parts
are unfinished, and it has not been battle-tested. use at your own risk,
especially since it can install and remove system packages.

a terminal ui for arch linux that searches both the official repositories and
the aur, then installs, updates and removes packages without leaving the app.

## what it does

- **search** packages across the official repos and the aur at once, ranked so
  the package you asked for is the first row and the related ones line up
  underneath it. the aur is asked over its own api, so a search is one request
  rather than a helper being started
- **package info** for whichever row the selection sits on: an outlined panel
  that opens below the list and takes most of the window, with the package
  name on the frame and then its description, version, license, maintainer,
  the dependencies it needs one per line (the ones already installed are
  marked) and the project page. pgup/pgdown read through a long panel
- **alternatives** listed inside that same view, underneath the package's own
  facts: the tools that do the same job, the ones that name it first
- **download** the selected package
- **uninstall** installed packages, with a filter to narrow the list
- **updates** shows what is waiting on you, pulled fresh from the repos and the
  aur, for one package or everything at once
- **arch news** in a column on the side: as many announcements as the column
  has room for. while a package is being read the column folds into one line
  holding the newest story, so the package keeps the width
- choose your installer between `pacman`, `yay` and `paru`
- runs the commands inside the app and streams their output into a log pane
- tabs on `f1` (search), `f2` (uninstall), `f3` (updates) and `f4` (settings)

## launching

```bash
gmai
```

that is it. the command is on your path in every new terminal.
