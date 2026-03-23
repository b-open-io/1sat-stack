/**
 * Raw legacy-to-legacy BSV and ordinal transfers.
 * Builds and signs transactions directly without wallet involvement.
 */

import { parseOutpoint } from "@1sat/utils";
import type { IndexedOutput } from "@1sat/types";
import { P2PKH, PrivateKey, Transaction, Utils } from "@bsv/sdk";
import { deriveAddress } from "./scanner";
import { getServices } from "./services";
import type { LegacyKeys } from "@/components/wif-input";

export interface LegacySendResult {
  txid: string;
  rawtx: string;
}

async function fetchSourceTx(txid: string): Promise<Transaction> {
  const services = getServices();
  const beef = await services.getBeefForTxid(txid);
  const found = beef.findTxid(txid);
  if (!found?.tx) throw new Error(`Transaction ${txid} not found in BEEF`);
  return found.tx;
}

/**
 * Build an address → PrivateKey map from the legacy keys.
 * Used to sign each input with the correct key based on the output's owner.
 */
function buildKeyMap(keys: LegacyKeys): Map<string, PrivateKey> {
  const map = new Map<string, PrivateKey>();
  const payKey = PrivateKey.fromWif(keys.payPk);
  const ordKey = PrivateKey.fromWif(keys.ordPk);
  map.set(deriveAddress(keys.payPk), payKey);
  map.set(deriveAddress(keys.ordPk), ordKey);
  if (keys.identityPk) {
    map.set(deriveAddress(keys.identityPk), PrivateKey.fromWif(keys.identityPk));
  }
  return map;
}

/**
 * Get the private key for an output by matching its events against the key map.
 * Looks for own: or p2pkh: event prefixes to determine the owner address.
 */
function keyForOutput(output: IndexedOutput, keyMap: Map<string, PrivateKey>, fallback: PrivateKey): PrivateKey {
  if (output.events) {
    for (const event of output.events) {
      if (event.startsWith("own:") || event.startsWith("p2pkh:")) {
        const addr = event.split(":")[1];
        const key = keyMap.get(addr);
        if (key) return key;
      }
    }
  }
  return fallback;
}

/**
 * Send BSV from legacy address to a destination address.
 * Builds a raw P2PKH transaction, signs with the appropriate key per input, broadcasts.
 */
export async function legacySendBsv(params: {
  funding: IndexedOutput[];
  keys: LegacyKeys;
  destination: string;
  amount?: number;
}): Promise<LegacySendResult> {
  const { funding, keys, destination, amount } = params;
  if (!funding.length) throw new Error("No funding UTXOs");
  if (!destination) throw new Error("No destination address");

  const keyMap = buildKeyMap(keys);
  const payKey = PrivateKey.fromWif(keys.payPk);
  const sourceAddress = payKey.toPublicKey().toAddress();
  const p2pkh = new P2PKH();
  const tx = new Transaction();

  for (const utxo of funding) {
    const { txid, vout } = parseOutpoint(utxo.outpoint);
    const key = keyForOutput(utxo, keyMap, payKey);
    tx.addInput({
      sourceTXID: txid,
      sourceOutputIndex: vout,
      sourceTransaction: await fetchSourceTx(txid),
      unlockingScriptTemplate: p2pkh.unlock(key),
      sequence: 0xffffffff,
    });
  }

  if (amount) {
    tx.addOutput({
      lockingScript: p2pkh.lock(destination),
      satoshis: amount,
    });
    tx.addOutput({
      lockingScript: p2pkh.lock(sourceAddress),
      change: true,
    });
  } else {
    tx.addOutput({
      lockingScript: p2pkh.lock(destination),
      change: true,
    });
  }

  await tx.fee();
  await tx.sign();

  const rawTx = tx.toBinary();
  const result = await getServices().arcade.submitTransaction(rawTx);

  return {
    txid: result.txid,
    rawtx: Utils.toHex(rawTx),
  };
}

/**
 * Send ordinals from legacy address to a destination address.
 * Each ordinal maps 1-sat-in to 1-sat-out. Funding UTXOs cover the fee.
 * Each input is signed with the key matching its owner address.
 */
export async function legacySendOrdinals(params: {
  ordinals: IndexedOutput[];
  funding: IndexedOutput[];
  keys: LegacyKeys;
  destination: string;
}): Promise<LegacySendResult> {
  const { ordinals, funding, keys, destination } = params;
  if (!ordinals.length) throw new Error("No ordinals to send");
  if (!funding.length) throw new Error("No funding UTXOs for fees");
  if (!destination) throw new Error("No destination address");

  const keyMap = buildKeyMap(keys);
  const payKey = PrivateKey.fromWif(keys.payPk);
  const sourceAddress = payKey.toPublicKey().toAddress();
  const p2pkh = new P2PKH();
  const tx = new Transaction();

  // Add ordinal inputs first — order matters for satoshi position mapping
  for (const ord of ordinals) {
    const { txid, vout } = parseOutpoint(ord.outpoint);
    const key = keyForOutput(ord, keyMap, payKey);
    tx.addInput({
      sourceTXID: txid,
      sourceOutputIndex: vout,
      sourceTransaction: await fetchSourceTx(txid),
      unlockingScriptTemplate: p2pkh.unlock(key),
      sequence: 0xffffffff,
    });
  }

  // Add 1-sat outputs positionally matched to ordinal inputs
  for (const _ord of ordinals) {
    tx.addOutput({
      lockingScript: p2pkh.lock(destination),
      satoshis: 1,
    });
  }

  // Add funding inputs
  for (const utxo of funding) {
    const { txid, vout } = parseOutpoint(utxo.outpoint);
    const key = keyForOutput(utxo, keyMap, payKey);
    tx.addInput({
      sourceTXID: txid,
      sourceOutputIndex: vout,
      sourceTransaction: await fetchSourceTx(txid),
      unlockingScriptTemplate: p2pkh.unlock(key),
      sequence: 0xffffffff,
    });
  }

  // Change output — fee() fills in the satoshis
  tx.addOutput({
    lockingScript: p2pkh.lock(sourceAddress),
    change: true,
  });

  await tx.fee();
  await tx.sign();

  const rawTx = tx.toBinary();
  const result = await getServices().arcade.submitTransaction(rawTx);

  return {
    txid: result.txid,
    rawtx: Utils.toHex(rawTx),
  };
}
