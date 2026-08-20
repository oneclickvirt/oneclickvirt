const escape = String.fromCharCode(27)
const cursorPositionReport = new RegExp(`^${escape}\\[\\d+;\\d+R`)
const cursorPositionReportPrefix = new RegExp(`^${escape}(?:\\[(?:\\d*(?:;\\d*)?)?)?$`)

// PVE serial guests can emit a terminal DSR request (ESC [ 6 n). xterm
// answers it locally with a cursor-position report, but a raw serial stream
// interprets that reply as guest input. Keep incomplete reports between input
// events as xterm is free to split them across callbacks.
export function createSerialConsoleInputFilter() {
  let remainder = ''

  return {
    filter(data) {
      if (typeof data !== 'string') return data

      const value = `${remainder}${data}`
      remainder = ''
      let filtered = ''
      let offset = 0

      // xterm is allowed to split the response at any byte boundary. Scan the
      // stream rather than replacing a whole message, retaining only a suffix
      // that could still become a cursor-position response.
      while (offset < value.length) {
        const escapeOffset = value.indexOf(escape, offset)
        if (escapeOffset < 0) {
          filtered += value.slice(offset)
          break
        }

        filtered += value.slice(offset, escapeOffset)
        const suffix = value.slice(escapeOffset)
        const report = suffix.match(cursorPositionReport)
        if (report) {
          offset = escapeOffset + report[0].length
          continue
        }
        if (cursorPositionReportPrefix.test(suffix)) {
          remainder = suffix
          break
        }

        filtered += escape
        offset = escapeOffset + 1
      }

      return filtered
    },
    hasPending() {
      return remainder !== ''
    },
    flush() {
      const pending = remainder
      remainder = ''
      return pending
    },
    reset() {
      remainder = ''
    }
  }
}
