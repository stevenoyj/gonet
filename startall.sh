#!/bin/sh
$GOPATH/bin/hub &
$GOPATH/bin/event &
$GOPATH/bin/stats &
$GOPATH/bin/agent
