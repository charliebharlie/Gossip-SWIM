package main

import (
	"time"
)

type NodeID struct {
	IP      string
	Port    int
	Version int
}

type NodeState string

const (
	Alive   NodeState = "Alive"
	Suspect NodeState = "Suspect"
	Dead    NodeState = "Dead"
)

type Member struct {
	ID          NodeID
	State       NodeState
	Heartbeat   int
	LastUpdate  time.Time // used locally for T_fail and T_suspicion
	Disseminate int       // counter for piggybacking, if a member has a positive value means we should send this member as an update
}

type Message struct {
	Type             string // "gossip", "join", "join_ack", "leave"
	SenderID         NodeID
	SenderState      NodeState
	SenderHeartbeat  int
	MembershipUpdate []Member // changes (suspect/dead/joins/leaves)
}
