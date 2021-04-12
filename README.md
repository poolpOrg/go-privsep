# privsep

WIP: This is work in progress, do not use for anything serious.

The privsep package provides a simple API to setup OpenBSD-style daemons,
relying on privileges separation and the fork+reexec pattern.

It allows describing the different processes and entry points,
as well as the IPC channels between them,
and takes care of all the underlying plumbing to bootstrap a daemon matching description.

It relies on the [github.com/poolpOrg/ipcmsg](https://github.com/poolpOrg/ipcmsg) package for IPC and fd passing.

For example of use, see the [example](https://github.com/poolpOrg/privsep/blob/main/example/example.go) program