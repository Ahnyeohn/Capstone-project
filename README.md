# Geth private network

## step 1. make genesis.json file
  path: build/bin/[path to store data]
  ex)

## step 2. apply genesis.json file to Geth
  command: ./build/bin/Geth --datadir [path to store data] init [path to genesis.json] 
  
## step 3. excute Geth
  command:  ./build/bin/Geth --networkid [your network id] --datadir [path to store data] --port [port number] --ipcdisable --nodiscover console --nat=extip:[your ip address] --allow-insecure-unlock
  
  ex) ./build/bin/Geth --networkid [your network id] --datadir [path to store data] --port [port number] --ipcdisable --nodiscover console --nat=extip:[your ip 
address] --allow-insecure-unlock

## step 4. make your Geth account and unlock your Account
  command: 
  1) personal.newAccount("[your password to use]")
  2) web3.personal.unlockAccount(eth.coinbase)
        
## step 5. connect with other peer
  In above, you need to get the enode information of the peer you want to connect to.
  command: admin.nodeInfo.enode 
  in this command, you can get peer's enode information.
  Next, connect with peer using enode info 
  command: admin.addPeer([enode value])

## step 6. Minining block
  command: miner.start([thread number])
  [thread number] means that the number of thread when mining
  ex) miner.start(2) or miner.start() for default: 1

  The moment a block is mined, the block is propagated to connected peers.
  
