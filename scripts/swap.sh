#!/bin/bash

read -p "Enter mode (gossip/swim): " MODE

case "$MODE" in
gossip)
	SIGNAL="SIGUSR1"
	;;
swim)
	SIGNAL="SIGUSR2"
	;;
*)
	echo "Invalid mode: $MODE"
	exit 1
	;;
esac

hosts=(
	fa25-cs425-a901.cs.illinois.edu
	fa25-cs425-a902.cs.illinois.edu
	fa25-cs425-a903.cs.illinois.edu
	fa25-cs425-a904.cs.illinois.edu
	fa25-cs425-a905.cs.illinois.edu
	fa25-cs425-a906.cs.illinois.edu
	fa25-cs425-a907.cs.illinois.edu
	fa25-cs425-a908.cs.illinois.edu
	fa25-cs425-a909.cs.illinois.edu
	fa25-cs425-a910.cs.illinois.edu
)

for host in "${hosts[@]}"; do
	echo "Switching $host to $MODE..."
	ssh -i ~/.ssh/id_ed25520 cliu132@"$host" bash -s <<EOF
pids=\$(lsof -t -i :5001)

if [ -n "\$pids" ]; then
  kill -$SIGNAL \$pids
  echo " -> Sent SIG$SIGNAL to PID(s): \$pids"
else
  echo " -> No process found on port 5001"
fi
EOF

done
