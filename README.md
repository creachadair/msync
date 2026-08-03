# msync

[![GoDoc](https://img.shields.io/static/v1?label=godoc&message=reference&color=green)](https://pkg.go.dev/github.com/creachadair/msync)
[![CI](https://github.com/creachadair/msync/actions/workflows/go-presubmit.yml/badge.svg?event=push&branch=main)](https://github.com/creachadair/msync/actions/workflows/go-presubmit.yml)

This repository defines a library of Go types to help in the management of
concurrency.

## Packages

- [throttle](https://godoc.org/github.com/creachadair/msync/throttle) a throttle for concurrent function calls ([code](./throttle))
- [trigger](https://godoc.org/github.com/creachadair/msync/trigger) an edge-triggered selectable condition variable ([code](./trigger))
