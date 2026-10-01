// Each asynchronous owner keeps its own generation. An old request may finish,
// but cannot change state or stop the next owner's timer.
export function createRequestGeneration() {
  let generation = 0
  return {
    next: () => ++generation,
    current: () => generation,
    isCurrent: token => token === generation
  }
}
