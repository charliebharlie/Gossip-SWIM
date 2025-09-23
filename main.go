package main

// TODO: Fix heartbeat counter and how nodes that left are joined back
import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"
)

var membershipList map[NodeID]Member

// Hyperparamter
var DEFAULT_DISSEMINATE int = 5

func main() {
	// Command Line Arguments
	ip := flag.String("ip", "127.0.0.1", "VM hostname")
	port := flag.Int("port", 5001, "Port to bind")
	introducer := flag.String("introducer", "", "IP:port of introducer (leave blank if current VM is introducer)")
	// mode := flag.String("mode", "gossip", "Gossip or SWIM")
	flag.Parse()

	membershipList = make(map[NodeID]Member)
	version, err := getVersion()
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}

	currNodeID := NodeID{IP: *ip, Port: *port}

	membershipList[currNodeID] = Member{
		ID:          currNodeID,
		Version:     version,
		State:       Alive,
		Heartbeat:   1,
		LastUpdate:  time.Now(),
		Disseminate: 0,
	}
	fmt.Printf("HERE: %v\n", membershipList[currNodeID])

	// Open UDP socket and listen for any writes to the socket
	addr := net.UDPAddr{IP: net.ParseIP(currNodeID.IP), Port: currNodeID.Port}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		fmt.Println("Error binding UDP:", err)
	}
	defer conn.Close()

	if *introducer != "" {
		host, portStr, _ := net.SplitHostPort(*introducer)
		port, _ := strconv.Atoi(portStr)
		introducerNode := NodeID{IP: host, Port: port}

		// send a join request to the introducer
		msg := Message{
			Type:   "join",
			Sender: membershipList[currNodeID],
		}

		data, err := json.Marshal(msg)
		if err != nil {
			fmt.Printf("Error handling Marshaling JSON: %v", err)
		}

		err = sendUDPto(introducerNode, data, conn)
		if err != nil {
			fmt.Printf("Failed to send UDP packet: %v", err)
		} else {
			fmt.Printf("Sent join request to introducer %v\n", introducerNode)
		}
	} else {
		fmt.Println("I am the introducer")
	}

	// Start receiver loop in the background
	go recvLoop(conn, currNodeID)

	// Start gossip loop in the background
	go gossipLoop(currNodeID, conn)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// runs in the background, listening for interrupt or terminated signals, once it receives a signal, call leaveGroup()
	<-sigChan
	leaveGroup(currNodeID, conn)
}

// recvLoop listens for incoming messages and logs them
func recvLoop(conn *net.UDPConn, currNodeID NodeID) {
	buf := make([]byte, 4096)
	for {
		fmt.Println("------------------------------------------------------")
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			fmt.Printf("recv error at address: %v: %v\n", addr, err)
			continue
		}

		var msg Message
		err = json.Unmarshal(buf[:n], &msg)
		if err != nil {
			fmt.Println("unmarshal error:", err)
			continue
		}

		fmt.Println("----------- BEFORE -----------")
		fmt.Printf("Received %s from Member: %v\nHeartbeat:%d\nState: %s\n", msg.Type, msg.Sender, msg.Sender.Heartbeat, msg.Sender.State)

		fmt.Printf("Current Table: %v\n", membershipList)
		// Join and Leave handling
		// Join: Only introducer and new node should use these messages
		// Leave: Leaving node sends it to a random node in the group
		switch msg.Type {
		case "join":

			// Add the new node to introducerNode's table
			membershipList[msg.Sender.ID] = Member{
				ID:          msg.Sender.ID,
				Version:     msg.Sender.Version,
				State:       Alive,
				Heartbeat:   1,
				LastUpdate:  time.Now(),
				Disseminate: DEFAULT_DISSEMINATE,
			}

			// Send back the full table of introducerNode
			fullTable := []Member{}
			for _, member := range membershipList {
				fullTable = append(fullTable, member)
			}
			fmt.Printf("FULLTABLE: %v", fullTable)

			// Send an ack to the new node, note that we don't set introducerNode as the sender, introducerNode is already in fullTable <- scratch this
			ack := Message{
				Type:             "join_ack",
				Sender:           membershipList[currNodeID], // for debugging purposes
				MembershipUpdate: fullTable,
			}
			data, _ := json.Marshal(ack)
			err := sendUDPto(msg.Sender.ID, data, conn)
			if err != nil {
				fmt.Printf("Failed to send UDP packet: %v", err)
			}

		case "join_ack":
			// Add the fullTable to the newNode's table
			for _, member := range msg.MembershipUpdate {
				updateMembershipList(member)
			}

		case "leave":
			fmt.Printf("Node %v leaving\n", msg.Sender.ID)
			m := membershipList[msg.Sender.ID]
			m.State = Dead
			m.LastUpdate = time.Now()
			m.Disseminate = DEFAULT_DISSEMINATE
			membershipList[msg.Sender.ID] = m

		case "gossip":
			// Refresh the sender node's entry in currNode's Membership List as we got a message from them
			updateMembershipList(msg.Sender)

			// Merge piggybacked updates
			for _, member := range msg.MembershipUpdate {
				updateMembershipList(member)
			}

		}

		fmt.Println("----------- After -----------")
		fmt.Printf("Received %s from Member: %v\nHeartbeat:%d\nState: %s\n", msg.Type, msg.Sender, msg.Sender.Heartbeat, msg.Sender.State)

		fmt.Printf("Current Table: %v\n", membershipList)
	}
}

func updateMembershipList(newNode Member) {
	oldNode, ok := membershipList[newNode.ID]

	// update the entry in membershipList if it doesn't exist or the version is lower
	if !ok || newNode.Version > oldNode.Version {
		membershipList[newNode.ID] = newNode
		fmt.Printf("Updated %v to %v\nState: %s\nVersion: %d \n", oldNode.ID, newNode.ID, newNode.State, newNode.Version)
		return
	}

	// if the versions are the same, we compare the heartbeats or states and take according to (alive < suspect < dead). TODO: If the newNode entry's State is Dead, we accept it no matter what. <--- Check this
	if newNode.Version == oldNode.Version {
		if (newNode.Heartbeat > oldNode.Heartbeat) || (newNode.Heartbeat == oldNode.Heartbeat && rank(newNode.State) > rank(oldNode.State) || (newNode.State == Dead)) {
			if newNode.Heartbeat > oldNode.Heartbeat {
				fmt.Printf("Merged %v into %v, because of larger heartbeat %d > %d\n", newNode.ID, oldNode.ID, newNode.Heartbeat, oldNode.Heartbeat)
			}
			if newNode.Heartbeat == oldNode.Heartbeat && rank(newNode.State) > rank(oldNode.State) {
				fmt.Printf("Merged %v into %v, because of higher state %s > %s\n", newNode.ID, oldNode.ID, newNode.State, oldNode.State)
			}

			if rank(newNode.State) != rank(oldNode.State) {
				// if the state is different, we update the TTL because a state change should be gossiped through the MembershipUpdate (piggybacking on heartbeats/acks)
				newNode.Disseminate = DEFAULT_DISSEMINATE
			}
			newNode.LastUpdate = time.Now()
			membershipList[newNode.ID] = newNode
		} else {
			oldNode.LastUpdate = time.Now()
			membershipList[newNode.ID] = oldNode
		}

	}
}

func rank(state NodeState) int {
	switch state {
	case (Alive):
		return 1
	case (Suspect):
		return 2
	case (Dead):
		return 3
	default:
		return 0
	}
}

func sendUDPto(receiverNode NodeID, data []byte, conn *net.UDPConn) error {
	addr := net.UDPAddr{IP: net.ParseIP(receiverNode.IP), Port: receiverNode.Port}
	_, err := conn.WriteToUDP(data, &addr)
	if err != nil {
		return fmt.Errorf("send error: %e", err)
	} else {
		fmt.Printf("Sent gossip to %v:%d\n", receiverNode.IP, receiverNode.Port)
		return nil
	}
}

func gossipLoop(currNodeID NodeID, conn *net.UDPConn) {
	ticker := time.NewTicker(1 * time.Second)
	for range ticker.C {
		currNode := membershipList[currNodeID]
		currNode.Heartbeat++
		membershipList[currNodeID] = currNode

		peer, err := getRandomNode(currNodeID)
		if err != nil {
			fmt.Printf("error: %v", err)
			continue
		}
		// piggyback any changes with the normal heartbeat
		piggyBackNodes := getPiggyBackNodes()
		if len(piggyBackNodes) != 0 {
			fmt.Printf("Changed or new nodes: %v", piggyBackNodes)
			// TODO: Finish this up next
			// os.Exit(1)
		}

		msg := Message{
			Type:             "gossip",
			Sender:           membershipList[currNodeID],
			MembershipUpdate: piggyBackNodes,
		}
		data, _ := json.Marshal(msg)
		err = sendUDPto(peer, data, conn)
		if err != nil {
			fmt.Printf("Failed to send UDP packet: %v", err)
		}
	}
}

// Gossip any nodes whose State has changed or any new nodes (will have a positive Disseminate counter)
func getPiggyBackNodes() []Member {
	changedNodes := []Member{}

	for id, member := range membershipList {
		if member.Disseminate > 0 {
			member.Disseminate--
			changedNodes = append(changedNodes, member)
			membershipList[id] = member
		}
	}
	return changedNodes
}

func getRandomNode(currNodeID NodeID) (NodeID, error) {
	candidates := []NodeID{}
	for id, m := range membershipList {
		if id == currNodeID || m.State == Dead {
			continue
		}
		candidates = append(candidates, id)
	}

	if len(candidates) == 0 {
		return NodeID{}, fmt.Errorf("Could not find a random peer\n")
	}
	return candidates[rand.Intn(len(candidates))], nil
}

func leaveGroup(currNode NodeID, conn *net.UDPConn) {
	leaveMsg := Message{
		Type:   "leave",
		Sender: membershipList[currNode],
	}
	data, _ := json.Marshal(leaveMsg)

	peer, err := getRandomNode(currNode)
	if err != nil {
		fmt.Printf("error: %v", err)
	} else {
		err = sendUDPto(peer, data, conn)
		if err != nil {
			fmt.Printf("Failed to send UDP packet: %v", err)
		}

		fmt.Printf("Sent leave message to %v, shutting down: %v\n", peer, currNode)
	}

	// Found online of how to flush stdout before exiting
	os.Stdout.Sync()
	os.Exit(0)
}

func getVersion() (int, error) {
	// hostname, err := os.Hostname()
	// if err != nil {
	// 	fmt.Printf("Could not find hostname\n")
	// }
	// vmNumber, err := getMachineNumber(hostname)
	// if err != nil {
	// 	fmt.Printf("Could not find machine number\n")
	// 	return 0, err
	// }

	// TODO: Test on local machine for now
	vmNumber := 0

	// TODO: Changed this back to /home/shared/incarnation_%d.log for testing on vms
	filePath := fmt.Sprintf("./incarnation_%d.log", vmNumber)
	data, err := os.ReadFile(filePath)
	if err != nil {
		// file doesn't exist yet, 0644 is the permission to create the file
		os.WriteFile(filePath, []byte("1"), 0644)
		return 1, nil
	}

	v, err := strconv.Atoi(string(data))
	if err != nil {
		fmt.Printf("Could not parse '%s': %v\n", filePath, err)
		fmt.Printf("Defaulting to 1...")
		v = 0
	}
	// increment for this incarnation of the vm
	v++

	os.WriteFile(filePath, []byte(strconv.Itoa(v)), 0644)
	return v, nil
}

// ex: input fa25-cs425-a901.cs.illinois.edu returns 1
func getMachineNumber(hostname string) (int, error) {
	re := regexp.MustCompile(`-(\d{3})\.`)
	match := re.FindStringSubmatch(hostname)
	if len(match) < 2 {
		return 0, fmt.Errorf("no machine number found in hostname: %s", hostname)
	}

	num, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, err
	}

	return num - 900, nil
}
