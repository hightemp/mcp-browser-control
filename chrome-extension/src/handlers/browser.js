export function createBrowserHandlers(now = () => new Date()) {
  return {
    ping() {
      return { pong: true, time: now().toISOString() };
    },
  };
}
