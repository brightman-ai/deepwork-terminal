/**
 * Shared shape between XtermTerminal.vue (owns the @xterm/addon-search wrapper: findNext /
 * findPrevious / clearSearch) and TerminalSearchBar.vue (owns the case/whole-word/regex toggle
 * UI). Kept in its own plain .ts module — not exported from either .vue's `<script setup>` —
 * so neither component has to import a type from the OTHER's SFC.
 */
export interface TerminalFindOptions {
  caseSensitive?: boolean
  wholeWord?: boolean
  regex?: boolean
}
