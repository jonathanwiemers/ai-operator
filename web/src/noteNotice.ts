// noteNotice says what became of a posted comment: '' when it was posted to
// GitLab; for Desk commands the server sent as chat commands instead (the
// comment box posts with the bot's token, whose commands the operator
// ignores), which ones went out.
import type { NoteCommandsResponse, NoteView } from './generated/views'

export function noteNotice(r: NoteView | NoteCommandsResponse | null | undefined): string {
  if (!r || !('routed' in r) || !r.routed?.length) return ''
  const what = r.routed.length === 1 ? 'a Desk command' : 'Desk commands'
  return `Sent ${r.routed.join(', ')} as ${what}, not posted to GitLab. The operator answers in the chat.`
}
