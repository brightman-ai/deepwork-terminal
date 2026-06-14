/**
 * touchballCursorGeometry — single source of truth for the VirtualTouchball cursor
 * arrow's pointing TIP, expressed in the SVG's local (18×22) coordinate space.
 *
 * Why this exists: the user aims copy-mode selection with the visible arrow TIP, not
 * the SVG box's top-left corner. The render (VirtualTouchball) offsets the SVG so the
 * tip lands on `cursorPosition`, and the copy-mode anchor (MobileOverlay) is mapped from
 * the SAME `cursorPosition`. Sharing these constants guarantees the rendered tip and the
 * mapped anchor cannot drift apart — the selection starts exactly under the visible cursor.
 *
 * The arrow path is `M2 2 L2 19 …` so its tip is at local (2, 2).
 */
export const CURSOR_TIP_X = 2
export const CURSOR_TIP_Y = 2
