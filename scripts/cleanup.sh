#!/bin/bash

# List of machines
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

PORT=5003

echo "Cleaning up nodes on port: $PORT"

for host in "${hosts[@]}"; do
	echo "Checking $host..."
	ssh -i ~/.ssh/id_ed25520 cliu132@"$host" bash -s <<EOF
PORT=$PORT
pids=\$(lsof -t -i :\$PORT)
if [ -n "\$pids" ]; then
    echo "Killing processes on port \$PORT: \$pids"
    kill -9 \$pids
else
    echo "No process using port \$PORT"
fi
EOF
done

echo "Cleanup complete."
