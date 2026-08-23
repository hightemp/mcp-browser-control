import { ErrorCode, protocolError } from "./protocol.js";

export async function initializeStoredState(
  storage,
  defaultSettings,
  randomUUID = () => crypto.randomUUID(),
) {
  const stored = await storage.get(["browserId", "settings"]);
  const updates = {};
  if (!stored.browserId) {
    updates.browserId = randomUUID();
  }
  if (!stored.settings) {
    updates.settings = { ...defaultSettings };
  }
  if (Object.keys(updates).length > 0) {
    await storage.set(updates);
  }
  return {
    browserId: stored.browserId || updates.browserId,
    settings: stored.settings || updates.settings,
  };
}

export async function getStoredIdentity(storage, defaultSettings, randomUUID) {
  const state = await initializeStoredState(storage, defaultSettings, randomUUID);
  return state.browserId;
}

export async function resetStoredIdentity(
  storage,
  confirmed,
  randomUUID = () => crypto.randomUUID(),
) {
  if (confirmed !== true) {
    throw protocolError(
      ErrorCode.CONFIRMATION_REQUIRED,
      "Resetting browser identity requires explicit confirmation",
    );
  }
  const browserId = randomUUID();
  await storage.set({ browserId });
  await storage.remove(["credential", "connectionDiagnostics"]);
  return browserId;
}
