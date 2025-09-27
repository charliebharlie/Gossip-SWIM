#!/bin/bash

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
	echo "Copying files to $host "
	scp -i ~/.ssh/id_ed25520 main.go membership.go go.mod cliu132@"$host":~/mp2/
done
