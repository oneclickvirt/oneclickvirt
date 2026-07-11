export const MOBILE_SIDEBAR_BREAKPOINT = 768
export const PORTRAIT_TABLET_SIDEBAR_BREAKPOINT = 1100

export function shouldUseSidebarDrawer(width, height) {
  const viewportWidth = Number(width)
  const viewportHeight = Number(height)
  if (!Number.isFinite(viewportWidth) || !Number.isFinite(viewportHeight)) {
    return false
  }

  return viewportWidth <= MOBILE_SIDEBAR_BREAKPOINT ||
    (viewportWidth <= PORTRAIT_TABLET_SIDEBAR_BREAKPOINT && viewportHeight > viewportWidth)
}
