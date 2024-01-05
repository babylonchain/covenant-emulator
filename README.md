# Covenant Emulator

## Overview
Bitcoin scripts have limited functions to control the authorization of a transaction.
Therefore, covenants are introduced as a means of restricting how funds may be spent including specifying how much must be spent and to what addresses.
Covenants are essentially a committee that co-sign Bitcoin transactions making sure the funds are sent as expected.
However, such a feature is not well-support in the current Bitcoin network.
To achieve a similar goal, we introduce the covenant emulator in our system to emulate Bitcoin covenants.

Covenant members are predetermined and their public keys are recorded in the genesis file of the Babylon chain.
Each member runs a `covenant-emulator` daemon (short for `covd`), which constantly monitors pending delegations on the Babylon chain and send signatures if they are valid delegations.
These delegations can only become active and empower the relevant finality provider until sufficient number of covenant members have sent their signatures.

Upon a pending delegation is found, the `covd` performs the following major checks:
1. the unbonding time should be not less than the minimum unbonding time required in the system,
2. all the params needed to build Bitcoin transactions are valid, and
3. the address that slashed funds will be sent to is the same as that of the genesis parameter.

If all the checks are passed, the `covd` will send three types of signatures to the Babylon chain:
1. **Staking signature**. This signature is an [adaptor signature](https://medium.com/crypto-garage/adaptor-signature-schnorr-signature-and-ecdsa-da0663c2adc4), which signs over the slashing path of the staking transaction.
Due to the uniqueness of the adaptor signature, it also prevents a malicious finality provider from irrationally slashing delegations.
2. **Unbonding signature**. This signature is a [Schnorr signature](https://en.wikipedia.org/wiki/Schnorr_signature), which is needed for the staker to withdraw their funds before the time lock.
3. **Unbonding slashing signature**. This signature is also an adaptor signature, which has similar usage of the **staking signature** but signs over the slashing path of the unbonding transaction.

