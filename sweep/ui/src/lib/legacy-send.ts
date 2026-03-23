/**
 * Raw legacy-to-legacy BSV and ordinal transfers.
 * Builds and signs transactions directly without wallet involvement.
 */

import { parseOutpoint } from "@1sat/utils";
import type { IndexedOutput } from "@1sat/types";
import { P2PKH, PrivateKey, Transaction, Utils } from "@bsv/sdk";
import { getServices } from "./services";

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
 * Send BSV from legacy address to a destination address.
 * Builds a raw P2PKH transaction, signs with WIF, broadcasts.
 */
export async function legacySendBsv(params: {
  funding: IndexedOutput[];
  wif: string;
  destination: string;
  amount?: number;
}): Promise<LegacySendResult> {
  const { funding, wif, destination, amount } = params;
  if (!funding.length) throw new Error("No funding UTXOs");
  if (!destination) throw new Error("No destination address");

  const privateKey = PrivateKey.fromWif(wif);
  const sourceAddress = privateKey.toPublicKey().toAddress();
  const p2pkh = new P2PKH();
  const tx = new Transaction();

  // Add funding inputs
  for (const utxo of funding) {
    const { txid, vout } = parseOutpoint(utxo.outpoint);
    tx.addInput({
      sourceTXID: txid,
      sourceOutputIndex: vout,
      sourceTransaction: await fetchSourceTx(txid),
      unlockingScriptTemplate: p2pkh.unlock(privateKey),
      sequence: 0xffffffff,
    });
  }

  // Add destination output
  const inputTotal = funding.reduce((sum, u) => sum + (u.satoshis ?? 0), 0);
  const sendAmount = amount ?? inputTotal; // fee() will adjust via change

  if (amount) {
    // Specific amount: explicit output + change
    tx.addOutput({
      lockingScript: p2pkh.lock(destination),
      satoshis: sendAmount,
    });
    tx.addOutput({
      lockingScript: p2pkh.lock(sourceAddress),
      change: true,
    });
  } else {
    // Max: destination gets everything minus fee
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
 */
export async function legacySendOrdinals(params: {
  ordinals: IndexedOutput[];
  funding: IndexedOutput[];
  wif: string;
  destination: string;
}): Promise<LegacySendResult> {
  const { ordinals, funding, wif, destination } = params;
  if (!ordinals.length) throw new Error("No ordinals to send");
  if (!funding.length) throw new Error("No funding UTXOs for fees");
  if (!destination) throw new Error("No destination address");

  const privateKey = PrivateKey.fromWif(wif);
  const sourceAddress = privateKey.toPublicKey().toAddress();
  const p2pkh = new P2PKH();
  const tx = new Transaction();

  // Add ordinal inputs first — order matters for satoshi position mapping
  for (const ord of ordinals) {
    const { txid, vout } = parseOutpoint(ord.outpoint);
    tx.addInput({
      sourceTXID: txid,
      sourceOutputIndex: vout,
      sourceTransaction: await fetchSourceTx(txid),
      unlockingScriptTemplate: p2pkh.unlock(privateKey),
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
    tx.addInput({
      sourceTXID: txid,
      sourceOutputIndex: vout,
      sourceTransaction: await fetchSourceTx(txid),
      unlockingScriptTemplate: p2pkh.unlock(privateKey),
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
