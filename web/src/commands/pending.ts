import type { Command } from "../api/commands";

const keyPrefix = "emsim.pending";

export interface PendingCommand {
  item_id: string;
  command: Command;
}

export function pendingKey(userId: string, itemId: string): string {
  return `${keyPrefix}.${userId}.${itemId}`;
}

export function availableLocalStorage(): Storage | null {
  try {
    const storage = window.localStorage;
    const probe = `${keyPrefix}.probe`;
    storage.setItem(probe, "1");
    storage.removeItem(probe);
    return storage;
  } catch {
    return null;
  }
}

export function loadPending(storage: Storage, userId: string, itemId: string): PendingCommand | null {
  try {
    const raw = storage.getItem(pendingKey(userId, itemId));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<PendingCommand>;
    if (parsed.item_id !== itemId || !parsed.command || typeof parsed.command.command_id !== "string") return null;
    return parsed as PendingCommand;
  } catch {
    return null;
  }
}

// Writes the exact command body before the network request. A different
// command cannot replace it: one item has at most one unresolved intent.
export function savePending(storage: Storage, userId: string, itemId: string, command: Command): PendingCommand {
  const existing = loadPending(storage, userId, itemId);
  if (existing && existing.command.command_id !== command.command_id) {
    throw new Error("another command is already pending for this item");
  }
  const pending = existing ?? { item_id: itemId, command };
  storage.setItem(pendingKey(userId, itemId), JSON.stringify(pending));
  return pending;
}

export function clearPending(storage: Storage, userId: string, itemId: string, commandId: string): void {
  const existing = loadPending(storage, userId, itemId);
  if (existing?.command.command_id === commandId) storage.removeItem(pendingKey(userId, itemId));
}
