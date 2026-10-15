# gmai

**work in progress** — this is an early build. things will change, some parts
are unfinished, and it has not been battle tested. use at your own risk,
especially since it can install and remove system packages.

a terminal ui for arch linux that searches both the official repositories and
the aur, then installs, updates and removes packages without leaving the app.

## what it does

- **search** packages across the official repos and the aur at once
- **download** the selected package
- **uninstall** installed packages, with a filter to narrow the list
- **updates** shows what is waiting on you, pulled fresh from the repos and the
  aur, for one package or everything at once
- choose your installer between `pacman`, `yay` and `paru`
- runs the commands inside the app and streams their output into a log pane

## launching

```bash
gmai
```

that is it. the command is on your path in every new terminal.
