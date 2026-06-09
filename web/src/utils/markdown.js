const allowedTags = new Set([
  'A', 'B', 'BLOCKQUOTE', 'BR', 'CODE', 'DIV', 'EM', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'HR', 'I', 'IMG',
  'LI', 'OL', 'P', 'PRE', 'S', 'SPAN', 'STRIKE', 'STRONG', 'SUB', 'SUP', 'U', 'UL'
])

const allowedAttrs = {
  A: new Set(['href', 'title', 'target', 'rel', 'aria-label']),
  IMG: new Set(['src', 'alt', 'title', 'width', 'height']),
  '*': new Set(['class'])
}

function isSafeUrl(value, allowImage = false) {
  try {
    const trimmed = String(value || '').trim()
    if (!trimmed) return false
    if (trimmed.startsWith('#') || trimmed.startsWith('/')) return true
    const url = new URL(trimmed, window.location.origin)
    if (['http:', 'https:'].includes(url.protocol)) return true
    if (allowImage && url.protocol === 'data:') return /^data:image\/(png|jpeg|jpg|gif|webp);base64,/i.test(trimmed)
    return false
  } catch (_) {
    return false
  }
}

function sanitizeHtml(html) {
  if (!html) return ''
  const tpl = document.createElement('template')
  tpl.innerHTML = html

  const walk = (node) => {
    if (node.nodeType === Node.ELEMENT_NODE) {
      if (!allowedTags.has(node.tagName)) {
        const children = Array.from(node.childNodes)
        node.replaceWith(...children)
        children.forEach(walk)
        return
      }
      for (const attr of Array.from(node.attributes)) {
        const key = attr.name.toLowerCase()
        const tagAllowed = allowedAttrs[node.tagName]?.has(key) || allowedAttrs['*'].has(key)
        if (!tagAllowed || key.startsWith('on') || key === 'style') {
          node.removeAttribute(attr.name)
          continue
        }
        if (key === 'href' && !isSafeUrl(attr.value)) node.removeAttribute(attr.name)
        if (key === 'src' && !isSafeUrl(attr.value, true)) node.removeAttribute(attr.name)
      }
      if (node.tagName === 'A') {
        node.setAttribute('rel', 'noopener noreferrer')
        if (!node.getAttribute('target')) node.setAttribute('target', '_blank')
      }
    }
    for (const child of Array.from(node.childNodes)) walk(child)
  }
  walk(tpl.content)
  return tpl.innerHTML
}

function inlineMarkdown(text) {
  return text
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\[([^\]]+)\]\(([^\s)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/(^|[^*])\*([^*]+)\*/g, '$1<em>$2</em>')
}

export function renderMarkdown(raw) {
  const value = String(raw || '').trim().replace(/\r\n/g, '\n')
  if (!value) return ''
  const lines = value.split('\n')
  const out = []
  let listType = null
  let code = false
  let codeLines = []

  const closeList = () => {
    if (listType) out.push(`</${listType}>`)
    listType = null
  }

  for (const line of lines) {
    const trimmed = line.trim()
    if (trimmed.startsWith('```')) {
      if (code) {
        out.push(`<pre><code>${codeLines.join('\n').replace(/[&<>]/g, ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[ch]))}</code></pre>`)
        codeLines = []
        code = false
      } else {
        closeList()
        code = true
      }
      continue
    }
    if (code) {
      codeLines.push(line)
      continue
    }
    if (!trimmed) {
      closeList()
      continue
    }
    if (/^[-_*]{3,}$/.test(trimmed)) {
      closeList()
      out.push('<hr>')
      continue
    }
    const heading = trimmed.match(/^(#{1,6})\s+(.+)$/)
    if (heading) {
      closeList()
      const level = heading[1].length
      out.push(`<h${level}>${inlineMarkdown(heading[2])}</h${level}>`)
      continue
    }
    if (trimmed.startsWith('> ')) {
      closeList()
      out.push(`<blockquote>${inlineMarkdown(trimmed.slice(2))}</blockquote>`)
      continue
    }
    if (/^[-*+]\s+/.test(trimmed)) {
      if (listType !== 'ul') {
        closeList()
        out.push('<ul>')
        listType = 'ul'
      }
      out.push(`<li>${inlineMarkdown(trimmed.slice(2).trim())}</li>`)
      continue
    }
    const ordered = trimmed.match(/^\d+\.\s+(.+)$/)
    if (ordered) {
      if (listType !== 'ol') {
        closeList()
        out.push('<ol>')
        listType = 'ol'
      }
      out.push(`<li>${inlineMarkdown(ordered[1])}</li>`)
      continue
    }
    closeList()
    out.push(`<p>${inlineMarkdown(trimmed)}</p>`)
  }
  closeList()
  if (code) out.push(`<pre><code>${codeLines.join('\n').replace(/[&<>]/g, ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[ch]))}</code></pre>`)
  return sanitizeHtml(out.join(''))
}
