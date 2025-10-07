# Ga9 - MP2


## Copy files to vms
To run, run 
``` bash
./scripts/copy.sh
``` 
to copy the files on each of the running vms. 
## Run 
Then, run 
```bash
 ./scripts/start.sh
 ``` 
 to start the algorithms (PingAck/Gossip). Customize shell script command to specify drop rate and input the introducer node

## Run commands
To input commands, run 
```bash 
./scripts/demo_command.sh
``` 
and enter command (ie. switch({Gossip | SWIM}, {suspect | nosuspect}))
to swap between Gossip/Swim and Suspicion/No Suspicion. Commands are as specified on Piazza.