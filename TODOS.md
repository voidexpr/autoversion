# Autoversion todos

## Restic repository next to the destination

`init --restic` creates `<parent>/.restic_<name>/repo` and
`<parent>/.restic_password_<name>` next to the destination, i.e. outside of
it. Move them inside the destination (none of the current jobs use restic).

## Configuration

~/projects
~/notes
~/Dropbox/Sync
~/dotfiles
