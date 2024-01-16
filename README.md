# Covenant Emulator

## Overview

Covenant emulator is a daemon program run by every member of the covenant 
committee of the BTC staking protocol. The role of the covenant committee 
is to protect PoS systems against attacks from the BTC stakers and 
validators. It achieves this by representing itself as an M-out-of-N 
multi-signature that co-signs BTC transactions with the BTC staker.

More specifically, through co-signing, the covenant committee enforces the 
following three spending rules of the staked bitcoins, the equivalence of 
which is common for PoS systems:

1. If the staker is malicious and gets slashed, the percentage of the slashed
bitcoins must satisfy the protocol's fractional slashing percentage.

2. If the staker is malicious and gets slashed, the destination address of the 
slashed bitcoins must be the unique slashing address specified by the 
protocol, not any other address.

3. when the staker unbonds, the unbonding time must be no shorter than the 
protocol's minimum stake unbonding time.

Besides enforcing rules via co-signing, the covenant committee has no other 
duty or power. If it has a dishonest super majority, then

* it can:

  * refuse to co-sign, so that no bitcoin holders can stake. In this case, 
    no bitcoin will be locked because the protocol requires the committee to 
    pre-sign all the transactions, and

  * collude with the stakers, so that the staker can dodge slashing.

* it cannot:
 
  * steal the staker’s bitcoins, because all the spending transactions
    require the staker's signature;
  
  * slash the staker’s bitcoins by itself, because slashing requires the 
    secret key of the finality provider, which the covenant committee does 
    not know in advance, and
   
  * prevent the staker from unbonding or withdrawing their bitcoins, again,
    because the protocol requires the committee to pre-sign all the transactions.

In other words, there is no way the committee can act against the stakers, 
except rejecting their staking requests. Furthermore, the dishonest actions 
of the covenant committee can be contained by 1) including the staker’s 
counterparty in the committee, such as the PoS system’s foundation, or 2) 
implementing a governance proposal to re-elect the committee.

This rule-enforcing committee is necessary for now because the current BTC 
protocol does not have the programmability needed to enforce these rules by 
code. This committee can be dimissed once such programmability becomes 
available, e.g., if BTC's covenant proposal [BIP-119](https://github.com/bitcoin/bips/blob/master/bip-0119.mediawiki)
is merged.

Covenant emulation committee members are defined in the Babylon parameters and
their public keys are recorded in the genesis file of the Babylon chain.
Changing the covenant committee requires a
[governance proposal](https://docs.cosmos.network/v0.50/build/modules/gov).
Each committee member runs the `covd` daemon (short for 
`covenant-emulator-daemon`), which
constantly monitors staking requests on the Babylon chain, verifies the 
validity of the Bitcoin transactions that are involved with them, and
sends the necessary signatures if verification is passed.
The staking requests can only become active and receive voting power
if a sufficient quorum of covenant committee members have
verified the validity of the transactions and sent corresponding signatures.

Upon a pending staking request being found, the covenant emulation daemon 
(`covd`), validates it against the spending rules defined in
[Staking Script specification](https://github.com/babylonchain/babylon-private/blob/dev/docs/staking-script.md),
and sends three types of signatures to the Babylon chain:

1. **Slashing signature**. This signature is an [adaptor signature](https://bitcoinops.org/en/topics/adaptor-signatures/),
which signs over the slashing path of the staking transaction. Due to the
[recoverability](https://github.com/LLFourn/one-time-VES/blob/master/main.pdf)
of the adaptor signature, it also prevents a malicious finality provider from
irrationally slashing delegations.
2. **Unbonding signature**. This signature is a [Schnorr signature](https://en.wikipedia.org/wiki/Schnorr_signature),
which is needed for the staker to unlock their funds before the original 
staking time lock expires (on-demand unbonding).
3. **Unbonding slashing signature**. This signature is also an adaptor
signature, which has similar usage to the **slashing signature** but signs over
the slashing path of the unbonding transaction.

## Installation

### Prerequisites

This project requires Go version `1.21` or later.
Install Go by following the instructions on
the [official Go installation guide](https://golang.org/doc/install).

#### Download the code

To get started, clone the repository to your local machine from Github:

```bash
$ git clone git@github.com:babylonchain/covenant-emulator.git
```

You can choose a specific version from
the [official releases page](https://github.com/babylonchain/covenant-emulator/releases):

```bash
$ cd covenant-emulator # cd into the project directory
$ git checkout <release-tag>
```

### Build and install the binary

At the top-level directory of the project

```bash
$ make install 
```

The above command will build and install the covenant-emulator daemon (`covd`)
binary to `$GOPATH/bin`:

If your shell cannot find the installed binaries, make sure `$GOPATH/bin` is in
the `$PATH` of your shell. Usually, these commands will do the job

```bash
export PATH=$HOME/go/bin:$PATH
echo 'export PATH=$HOME/go/bin:$PATH' >> ~/.profile
```

To build without installing,

```bash
$ make build
```

The above command will put the built binaries in a build directory with the
following structure:

```bash
$ ls build
    └── covd
```

Another common issue with compiling is that some of the dependencies have
components written in C. If a C toolchain is absent, the Go compiler will throw
errors. (Most likely it will complain about undefined names/types.) Make sure a
C toolchain (for example, GCC or Clang) is available. On Ubuntu, this can be
installed by running

```bash
sudo apt install build-essential
```

## Setting up a covenant emulator

### Configuration

The `covd init` command initializes a home directory for the
finality provider daemon.
This directory is created in the default home location or in a
location specified by the `--home` flag.
If the home directory already exists, add `--force` to override the directory if
needed.

```bash
$ covd init --home /path/to/covd/home/
```

After initialization, the home directory will have the following structure

```bash
$ ls /path/to/covd/home/
  ├── covd.conf # Covd-specific configuration file.
  ├── logs      # Covd logs
```

If the `--home` flag is not specified, then the default home directory
will be used. For different operating systems, those are:

- **MacOS** `~/Users/<username>/Library/Application Support/Covd`
- **Linux** `~/.Covd`
- **Windows** `C:\Users\<username>\AppData\Local\Covd`

Below are some important parameters of the `covd.conf` file.

**Note**:
The configuration below requires to point to the path where this keyring is
stored `KeyDirectory`. This `Key` field stores the key name used for interacting
with the Babylon chain and will be specified along with the `KeyringBackend`
field in the next [step](#generate-key-pairs). So we can ignore the setting of
the two fields in this step.

```bash
# The interval between each query for pending BTC delegations
QueryInterval = 15s

# The maximum number of delegations that the covd processes each time
DelegationLimit = 100

# Bitcoin network to run on
BitcoinNetwork = simnet

# Babylon specific parameters

# Babylon chain ID
ChainID = chain-test

# Babylon node RPC endpoint
RPCAddr = http://127.0.0.1:26657

# Babylon node gRPC endpoint
GRPCAddr = https://127.0.0.1:9090

# Name of the key in the keyring to use for signing transactions
Key = <finality-provider-key-name>

# Type of keyring to use,
# supported backends - (os|file|kwallet|pass|test|memory)
# ref https://docs.cosmos.network/v0.46/run-node/keyring.html#available-backends-for-the-keyring
KeyringBackend = test

# Directory where keys will be retrieved from and stored
KeyDirectory = /path/to/covd/home
```

To see the complete list of configuration options, check the `covd.conf` file.

## Generate key pairs

The covenant emulator daemon requires the existence of a keyring that signs
signatures and interacts with Babylon. Use the following command to generate the
key:

```bash
$ covd create-key --key-name covenant-key --chain-id chain-test
{
    "name": "cov-key",
    "public-key": "9bd5baaba3d3fb5a8bcb8c2995c51793e14a1e32f1665cade168f638e3b15538"
}
```

After executing the above command, the key name will be saved in the config file
created in [step](#configuration).
Note that the `public-key` in the output should be used as one of the inputs of
the genesis of the Babylon chain.
Also, this key will be used to pay for the fees due to the daemon submitting 
signatures to Babylon.

## Start the daemon

You can start the covenant emulator daemon using the following command:

```bash
$ covd start
2024-01-05T05:59:09.429615Z	info	Starting Covenant Emulator
2024-01-05T05:59:09.429713Z	info	Covenant Emulator Daemon is fully active!
```

All the available CLI options can be viewed using the `--help` flag. These
options can also be set in the configuration file.
