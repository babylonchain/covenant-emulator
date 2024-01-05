# Covenant Emulator

## Overview
Bitcoin scripts have limited functions to control the authorization of a transaction.
Therefore, covenants are introduced as a means of restricting how funds may be spent including specifying how much must be spent and to what addresses.
Covenants are essentially a committee that co-sign Bitcoin transactions making sure the funds are sent as expected.
However, such a feature is not well-supported in the current Bitcoin network.
To achieve a similar goal, we introduce the covenant emulator in our system to emulate Bitcoin covenants.

Covenant members are predetermined and their public keys are recorded in the genesis file of the Babylon chain.
Each member runs a `covenant-emulator` daemon (short for `covd`), which constantly monitors pending delegations on the Babylon chain and sends signatures if they are valid delegations.
These delegations can only become active and empower the relevant finality provider until a sufficient number of covenant members have sent their signatures.

Upon a pending delegation is found, the `covd` performs the following major checks:
1. the unbonding time should be not less than the minimum unbonding time required in the system,
2. all the params needed to build Bitcoin transactions are valid, and
3. the address to which slashed funds will be sent is the same as that of the genesis parameter.

If all the checks are passed, the `covd` will send three types of signatures to the Babylon chain:
1. **Staking signature**. This signature is an [adaptor signature](https://medium.com/crypto-garage/adaptor-signature-schnorr-signature-and-ecdsa-da0663c2adc4), which signs over the slashing path of the staking transaction.
   Due to the uniqueness of the adaptor signature, it also prevents a malicious finality provider from irrationally slashing delegations.
2. **Unbonding signature**. This signature is a [Schnorr signature](https://en.wikipedia.org/wiki/Schnorr_signature), which is needed for the staker to withdraw their funds before the time lock.
3. **Unbonding slashing signature**. This signature is also an adaptor signature, which has similar usage to the **staking signature** but signs over the slashing path of the unbonding transaction.

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
the [official releases page](https://github.com/babylonchain/covenant-emulator/releases)

```bash
$ cd covenant-emulator # cd into the project directory
$ git checkout <release-tag>
```

### Build and install the binary

At the top-level directory of the project

```bash
$ make install 
```

The above command will build and install the covenant-emulator daemon (`covd`) binary to
`$GOPATH/bin`:

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
C toolchain (for example, GCC or Clang) is available.
On Ubuntu, this can be
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
If the home direction already exists, add `--force` to override the directory if needed.

```bash
$ covd init --home /path/to/covd/home/
```

After initialization, the home directory will have the following structure

```bash
$ ls /path/to/covd/home/
  ├── covd.conf # Covd-specific configuration file.
  ├── logs     # Covd logs
```

If the `--home` flag is not specified, then the default home directory
will be used. For different operating systems, those are:

- **MacOS** `~/Users/<username>/Library/Application Support/Covd`
- **Linux** `~/.Covd`
- **Windows** `C:\Users\<username>\AppData\Local\Covd`

Below are some important parameters of the `covd.conf` file.

**Note**:
The configuration below requires to point to the path where this keyring is stored `KeyDirectory`.
This `Key` field stores the key name used for interacting with the Babylon chain
and will be specified along with the `KeyringBackend` field in the next [step](#generate-key-pairs).
So we can ignore the setting of the two fields in this step.

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

The covenant emulator daemon requires the existence of a keyring that signs signatures and interacts with Babylon.
Use the following command to generate the key:

```bash
$ covd create-key --key-name covenant-key --chain-id chain-test
{
    "name": "cov-key",
    "public-key": "9bd5baaba3d3fb5a8bcb8c2995c51793e14a1e32f1665cade168f638e3b15538"
}
```

After executing the above command, the key name will be saved in the config file
created in [step](#configuration).
Note that the `public-key` in the output should be used as one of the inputs of the genesis of the Babylon chain.

## Start the daemon

You can start the covenant emulator daemon using the following command:

```bash
$ covd start
2024-01-05T05:59:09.429615Z	info	Starting Covenant Emulator
2024-01-05T05:59:09.429713Z	info	Covenant Emulator Daemon is fully active!
```

All the available CLI options can be viewed using the `--help` flag. These options
can also be set in the configuration file.
