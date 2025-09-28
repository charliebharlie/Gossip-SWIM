package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"
)

var membershipList map[NodeID]Member
var pendingList map[NodeID]Pending
var indirectPingTracking map[NodeID]NodeID
var mu sync.RWMutex
var suspicionTimers map[NodeID]chan struct{}
var gossipAliveTimers map[NodeID]chan struct{}
var cancel context.CancelFunc

// Hyperparamters
var delta int = 1
var DEFAULT_DISSEMINATE int = 5
var NUM_RANDOM_INDIRECT_PINGS int = 1
var T_Gossip_Suspicion = time.Duration(1*delta) * time.Second
var T_Gossip_Fail = time.Duration(2*delta) * time.Second

var drop_rate int = 10

var T_SWIM_Direct = time.Duration(500*delta) * time.Millisecond
var T_SWIM_Indirect = time.Duration(1000*delta) * time.Millisecond
var T_SWIM_Suspicion = T_SWIM_Direct + T_SWIM_Indirect
var T_SWIM_Fail = time.Duration(1*delta)*time.Second + time.Duration(500*delta)*time.Millisecond

func main() {
	// fmt.Println(float64(T_SWIM_Fail)/float64(time.Second), float64(T_SWIM_Suspicion)/float64(time.Second))
	// os.Exit(0)

	// Command Line Arguments
	hostname, err := os.Hostname()
	if err != nil {
		fmt.Printf("Could not find hostname\n")
	}

	ip := flag.String("ip", hostname, "VM hostname")
	// port := flag.Int("port", 5001, "Port to bind")

	// VM testing
	port := 5001
	introducer := flag.String("introducer", "", "IP:port of introducer (leave blank if current VM is introducer)")
	dropRate := flag.Int("drop-rate", 0, "Rate at which recieved packets are dropped")
	drop_rate = *dropRate

	flag.Parse()

	membershipList = make(map[NodeID]Member)
	pendingList = make(map[NodeID]Pending)
	indirectPingTracking = make(map[NodeID]NodeID)
	suspicionTimers = make(map[NodeID]chan struct{})
	gossipAliveTimers = make(map[NodeID]chan struct{})

	version, err := getVersion()
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}

	currNodeID := NodeID{IP: *ip, Port: port}

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
	addr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
	conn, err := net.ListenUDP("udp", addr)
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
			Type:             "join",
			Sender:           membershipList[currNodeID],
			MembershipUpdate: []Member{},
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
	switchTo("Gossip", currNodeID, conn)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGINT)

	for {
		// runs in the background, listening for one of the signals, once it receives a signal, execute the code inside the scope
		s := <-sigs
		switch s {
		case syscall.SIGUSR1:
			fmt.Println("Switching to Gossip")
			switchTo("Gossip", currNodeID, conn)

		case syscall.SIGUSR2:
			fmt.Println("Switching to SWIM")
			switchTo("SWIM", currNodeID, conn)

		case syscall.SIGINT:
			fmt.Println("Leaving group...")
			leaveGroup(currNodeID, conn)
			return
		}
	}
}

func switchTo(mode string, currNodeID NodeID, conn *net.UDPConn) {
	if cancel != nil {
		cancel() // stop old loop
	}

	ctx, currentCancel := context.WithCancel(context.Background())
	cancel = currentCancel

	if mode == "Gossip" {
		go gossipLoop(ctx, currNodeID, conn)
	} else if mode == "SWIM" {
		go swimLoop(ctx, currNodeID, conn)
	}
}

// recvLoop listens for incoming messages and logs them
func recvLoop(conn *net.UDPConn, currNodeID NodeID) {
	buf := make([]byte, 4096)
	for {
		fmt.Println("------------------------------------------------------------------------------")
		n, addr, err := conn.ReadFromUDP(buf)
		random_drop := rand.Intn(100)
		if random_drop < drop_rate {
			continue
		}

		if err != nil {
			fmt.Printf("recv error: %v at address: %v\n", err, addr)
			continue
		}

		var msg Message
		err = json.Unmarshal(buf[:n], &msg)
		if err != nil {
			fmt.Println("unmarshal error:", err)
			continue
		}

		fmt.Println(msg)
		fmt.Println("----------- BEFORE -----------")
		fmt.Println("Current Table: ")
		for _, member := range membershipList {
			fmt.Println(member)
		}

		// Join and Leave handling
		// Join: Only introducer and new node should use these messages
		// Leave: Leaving node sends it to any random node in the group
		switch msg.Type {
		case "join":
			fmt.Println("Recevied Join from ", msg.Sender)

			mu.Lock()

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

			ack := Message{
				Type:             "join_ack",
				Sender:           membershipList[currNodeID],
				MembershipUpdate: fullTable,
			}

			data, _ := json.Marshal(ack)
			err := sendUDPto(msg.Sender.ID, data, conn)
			if err != nil {
				fmt.Printf("Failed to send UDP packet: %v", err)
			}
			mu.Unlock()

		case "join_ack":
			// Add the fullTable to the newNode's table
			for _, member := range msg.MembershipUpdate {
				updateMembershipList(member)
			}

		case "leave":
			mu.Lock()
			fmt.Printf("Node %v leaving\n", msg.Sender.ID)
			currNode := membershipList[msg.Sender.ID]
			currNode.State = Dead
			currNode.LastUpdate = time.Now()
			currNode.Disseminate = DEFAULT_DISSEMINATE
			membershipList[msg.Sender.ID] = currNode
			mu.Unlock()

		case "gossip":
			// Refresh the sender node's entry in currNode's Membership List as we got a message from them
			updateMembershipList(msg.Sender)

			// Merge piggybacked updates
			for _, member := range msg.MembershipUpdate {
				updateMembershipList(member)
			}

			refreshGossipSuspicion(msg.Sender.ID)

		case "swim_ping":
			// this node was pinged and need to respond
			ack := Message{
				Type:             "swim_ack",
				Sender:           membershipList[currNodeID],
				MembershipUpdate: msg.MembershipUpdate,
			}

			data, _ := json.Marshal(ack)
			err := sendUDPto(msg.Sender.ID, data, conn)
			if err != nil {
				fmt.Printf("Failed to send UDP packet: %v", err)
			}
			updateMembershipList(msg.Sender)

			for _, member := range msg.MembershipUpdate {
				updateMembershipList(member)
			}

		case "swim_ack":
			// in indirectPinging, the indirect node receives the swim_ack from the target node

			// this node pinged a node and this is the respones
			// remove from pending list
			senderID := msg.Sender.ID
			_, ok1 := pendingList[senderID]
			_, ok2 := indirectPingTracking[senderID]

			if ok1 {
				// Delete from our current list of pending nodes
				delete(pendingList, senderID)
			}
			if ok2 {
				// Delete from our parent node's list of pending nodes
				msg := Message{
					Type:             "swim_indirect_ack",
					Sender:           membershipList[senderID],
					MembershipUpdate: msg.MembershipUpdate,
				}

				data, _ := json.Marshal(msg)
				// send to target node
				err := sendUDPto(indirectPingTracking[senderID], data, conn)
				if err != nil {
					fmt.Printf("Failed to send UDP packet: %v", err)
				}
			}

			if !ok1 && !ok2 {
				os.Exit(1)
			}
			updateMembershipList(msg.Sender)

			for _, member := range msg.MembershipUpdate {
				updateMembershipList(member)
			}

		case "swim_indirect_ping":
			// ping node based on directions of another node
			targetNodeID := msg.TargetID // add
			ping := Message{
				Type:             "swim_ping",
				Sender:           membershipList[currNodeID],
				MembershipUpdate: msg.MembershipUpdate,
			}

			// indirect node's indirectPingTracking[target node] = original node
			indirectPingTracking[targetNodeID] = msg.Sender.ID
			data, _ := json.Marshal(ping)
			// send to target node
			err := sendUDPto(targetNodeID, data, conn)
			if err != nil {
				fmt.Printf("Failed to send UDP packet: %v", err)
			}
			updateMembershipList(msg.Sender)

			for _, member := range msg.MembershipUpdate {
				updateMembershipList(member)
			}

		case "swim_indirect_ack":
			// send ack to node that wanted this node to ping another node
			// ack := Message{
			// 	Type:             "swim_ack",
			// 	Sender:           msg.Sender,
			// 	MembershipUpdate: []Member{},
			// }
			//
			// data, _ := json.Marshal(ack)
			// // send back to whoever wanted this to be sent
			// err := sendUDPto(indirectPingTracking[msg.Sender.ID], data, conn)
			// if err != nil {
			// 	fmt.Printf("Failed to send UDP packet: %v", err)
			// }

			senderID := msg.Sender.ID
			delete(pendingList, senderID)
			updateMembershipList(msg.Sender)

			for _, member := range msg.MembershipUpdate {
				updateMembershipList(member)
			}
		}

		fmt.Println("----------- AFTER -----------")
		// fmt.Println(msg.Sender)

		fmt.Println("Current Table: ")
		for _, member := range membershipList {
			fmt.Println(member)
		}

	}
}

func updateMembershipList(newNode Member) {
	mu.Lock()
	defer mu.Unlock()
	oldNode, exists := membershipList[newNode.ID]

	// update the entry in membershipList if it doesn't exist or the version is lower
	if !exists || newNode.Version > oldNode.Version {
		newNode.LastUpdate = time.Now()
		membershipList[newNode.ID] = newNode

		// bug here... if we don't delete the Dead node from the pendingList after it comes back, the PingState remains the same, so it'll always be suspect...
		delete(pendingList, newNode.ID)

		fmt.Printf("Updated %v to %v\nState: %s\nVersion: %d \n", oldNode.ID, newNode.ID, newNode.State, newNode.Version)
		return
	}

	// if the versions are the same, we compare the heartbeats or states and take according to (alive < suspect < dead). TODO: If the newNode entry's State is Dead, we accept it no matter what. <--- Check this (not sure why, the system works much better for NOT taking Dead state nodes no matter what [ie. we consider a larger Heartbeat value instead])
	if newNode.Version == oldNode.Version {
		if (newNode.Heartbeat > oldNode.Heartbeat) || (newNode.Heartbeat == oldNode.Heartbeat && rank(newNode.State) > rank(oldNode.State)) {
			if newNode.Heartbeat > oldNode.Heartbeat {
				fmt.Printf("Heartbeat Merged %v into %v\n", newNode, oldNode)
			}
			if newNode.Heartbeat == oldNode.Heartbeat && rank(newNode.State) > rank(oldNode.State) {
				fmt.Printf("State Merged %v into %v\n", newNode, oldNode)
			}

			if rank(newNode.State) != rank(oldNode.State) || newNode.Heartbeat > oldNode.Heartbeat {
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
		return -1
	}
}

func refreshGossipSuspicion(currNodeID NodeID) {
	mu.Lock()
	defer mu.Unlock()

	// If the stop channel already exists, that means we should reset the timer (the timer being either the timer before suspicion or the timer after suspicion) as we heard from the suspected node
	stopChannel, exists := gossipAliveTimers[currNodeID]
	if exists {
		close(stopChannel)
		delete(gossipAliveTimers, currNodeID)
	}

	// We want to interrupt the timers if the channel is closed (ie. we received a heartbeat from the suspected node)
	stop := make(chan struct{})
	gossipAliveTimers[currNodeID] = stop

	go func(currNodeID NodeID, stop <-chan struct{}) {
		select {
		case <-time.After(T_Gossip_Suspicion):
			mu.Lock()
			currNode := membershipList[currNodeID]
			if currNode.State == Alive {
				currNode.State = Suspect
				currNode.Disseminate = DEFAULT_DISSEMINATE
				membershipList[currNodeID] = currNode

				fmt.Printf("Node %v is suspected\n", currNodeID)
				go startFailTimer(currNodeID, T_Gossip_Fail)
			}
			mu.Unlock()
		case <-stop:
			return
		}
	}(currNodeID, stop)
}

// Both Gossip and SWIM have the same suspicion -> Dead logic
func startFailTimer(currNodeID NodeID, failTimeout time.Duration) {
	mu.Lock()
	defer mu.Unlock()
	stopChannel, exists := suspicionTimers[currNodeID]

	// If the stop channel already exists, that means we should reset the timer (the timer being either the timer before suspicion or the timer after suspicion) as we heard from the suspected node
	if exists {
		close(stopChannel)
		delete(suspicionTimers, currNodeID)
	}

	// We want to interrupt the timers if the channel is closed (ie. we received a heartbeat from the suspected node)
	stop := make(chan struct{})
	suspicionTimers[currNodeID] = stop

	go func(currNodeID NodeID, stop <-chan struct{}) {
		select {
		case <-time.After(failTimeout):
			mu.Lock()
			currNode := membershipList[currNodeID]
			if currNode.State == Suspect {
				currNode.State = Dead
				currNode.Disseminate = DEFAULT_DISSEMINATE
				membershipList[currNodeID] = currNode

				fmt.Printf("Node %v is Dead\n", currNodeID)
			}
			mu.Unlock()
		case <-stop:
			return
		}
	}(currNodeID, stop)
}

func sendUDPto(receiverNode NodeID, data []byte, conn *net.UDPConn) error {
	addr, _ := net.ResolveUDPAddr("udp", net.JoinHostPort(receiverNode.IP, strconv.Itoa(receiverNode.Port)))
	_, err := conn.WriteToUDP(data, addr)
	if err != nil {
		return fmt.Errorf("send error: %e", err)
	} else {
		fmt.Printf("Sent to %v\n", receiverNode)
		return nil
	}
}

func gossipLoop(ctx context.Context, currNodeID NodeID, conn *net.UDPConn) {
	// Tuning how often the nodes should gossip
	ticker := time.NewTicker(time.Duration(1000*delta) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("Stopping gossip loop")
			return

		case <-ticker.C:
			mu.Lock()
			currNode := membershipList[currNodeID]
			currNode.Heartbeat++
			membershipList[currNodeID] = currNode
			mu.Unlock()

			peer, err := getRandomNode(currNodeID)
			if err != nil {
				fmt.Printf("error: %v", err)
				continue
			}

			// piggyback any changes with the normal heartbeat
			piggyBackNodes := getPiggyBackNodes()

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
}

func swimLoop(ctx context.Context, currNodeID NodeID, conn *net.UDPConn) {
	// here just send and add to a waiting list with a specified timeout, before sending new ping in this loop check waiting list to see if any have been marked recieved
	// by recv_loop. If yes, chilling. If no and more than timeout away from og time, ping random neighbors, update awaiting entry? if no receipt within
	// another timeout length time, then mark as dead?
	ticker := time.NewTicker(time.Duration(500*delta) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println("Stopping gossip loop")
			return

		case <-ticker.C:
			// keep track of membership list in case switched back to gossip
			mu.Lock()
			currNode := membershipList[currNodeID]
			currNode.Heartbeat++
			membershipList[currNodeID] = currNode
			mu.Unlock()

			// piggyback any changes with the normal heartbeat
			piggyBackNodes := getPiggyBackNodes()

			for targetID, _ := range pendingList {
				if time.Now().Sub(pendingList[targetID].SentTime) > T_SWIM_Direct && pendingList[targetID].PingState == FirstPing {
					// send indirect pings
					for i := 0; i < NUM_RANDOM_INDIRECT_PINGS; i++ {
						peerID, err := getRandomNode(currNodeID)
						if peerID == targetID {
							continue
						}

						if err != nil {
							fmt.Printf("error: %v", err)
							continue
						}

						mu.RLock()
						msg := Message{
							Type:             "swim_indirect_ping",
							Sender:           membershipList[currNodeID],
							MembershipUpdate: piggyBackNodes,
							TargetID:         targetID,
						}
						mu.RUnlock()

						// update pending entry to be (target peer, new time(?), second ping)
						pending := pendingList[targetID]
						pending.PingState = SecondPing
						pending.SentTime = time.Now()
						pendingList[targetID] = pending
						fmt.Printf("Sending indirect ping to target: %v\n", targetID)

						data, _ := json.Marshal(msg)
						err = sendUDPto(peerID, data, conn)
						if err != nil {
							fmt.Printf("Failed to send UDP packet: %v", err)
						}
					}

				} else if time.Now().Sub(pendingList[targetID].SentTime) > T_SWIM_Indirect && pendingList[targetID].PingState == SecondPing {
					// if the T_SWIM_Fail times out, the node is dead
					mu.Lock()
					targetNode, ok := membershipList[targetID]
					if ok {
						// mark as suspected
						if targetNode.State == Alive {
							targetNode.State = Suspect
							targetNode.Disseminate = DEFAULT_DISSEMINATE
							membershipList[targetID] = targetNode
							fmt.Printf("Node %v is suspected\n", targetID)
							go startFailTimer(targetID, T_SWIM_Fail)
							// delete(pendingList, targetID)
						}
					}
					mu.Unlock()
					fmt.Printf("TIME: %v\n", time.Now().Sub(pendingList[targetID].SentTime))

				}
			}

			piggyBackNodes = getPiggyBackNodes()
			targetID, err := getRandomNode(currNodeID)
			if err != nil {
				fmt.Printf("error: %v", err)
				continue
			}

			mu.RLock()
			msg := Message{
				Type:             "swim_ping",
				Sender:           membershipList[currNodeID],
				MembershipUpdate: piggyBackNodes,
				TargetID:         targetID,
			}
			mu.RUnlock()

			// add (peer, time, state) to list where state can be first_ping, second_ping?
			pendingList[targetID] = Pending{
				ID:        targetID,
				PingState: FirstPing,
				SentTime:  time.Now(),
			}

			data, _ := json.Marshal(msg)
			err = sendUDPto(targetID, data, conn)
			if err != nil {
				fmt.Printf("Failed to send UDP packet: %v", err)

			}
		}
	}
}

// Gossip any nodes whose State has changed or any new nodes (will have a positive Disseminate counter)
func getPiggyBackNodes() []Member {
	mu.Lock()
	defer mu.Unlock()

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
	mu.RLock()
	defer mu.RUnlock()

	candidates := []NodeID{}
	for id, member := range membershipList {
		_, exists := pendingList[id]
		if id == currNodeID || exists || member.State == Dead {
			continue
		}

		candidates = append(candidates, id)
	}

	if len(candidates) == 0 {
		return NodeID{}, fmt.Errorf("Could not find a random node\n")
	}

	return candidates[rand.Intn(len(candidates))], nil
}

// voluntarily leaves the group
func leaveGroup(currNode NodeID, conn *net.UDPConn) {
	mu.RLock()
	defer mu.RUnlock()

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
	hostname, err := os.Hostname()
	if err != nil {
		fmt.Printf("Could not find hostname\n")
	}
	vmNumber, err := getMachineNumber(hostname)
	if err != nil {
		fmt.Printf("Could not find machine number\n")
		return 0, err
	}

	// TODO: Test on local machine for now
	// vmNumber := 0

	// TODO: Changed this back to /home/shared/incarnation_%d.log for testing on vms
	filePath := fmt.Sprintf("/home/cliu132/mp2/incarnation_%d.log", vmNumber)
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
	re := regexp.MustCompile(`-a(\d{3})\.`)
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
