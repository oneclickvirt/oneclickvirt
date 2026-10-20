// An old image response must never replace the list for a newer node/type.
export function createImageRequestGuard(readSelection) {
  let sequence = 0
  return {
    invalidate() {
      sequence += 1
    },
    begin() {
      const request = ++sequence
      const selection = readSelection()
      return () => request === sequence && selection === readSelection()
    }
  }
}
