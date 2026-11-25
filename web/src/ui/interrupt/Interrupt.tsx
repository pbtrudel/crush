import { needsAttention } from '../../state/reducer';
import { state } from '../../state/store';
import { PermissionDialog } from './PermissionDialog';
import { QuestionForm } from './QuestionForm';
import { SecurePromptDialog } from './SecurePrompt';

// Interrupt renders whenever App has a reason to inert the shell, so a dialog
// is on screen for as long as the background is locked. A secure prompt
// blocks the running command, so it wins over a permission request and a
// question. A touch prompt is a notice the terminal answers itself, so it
// never takes the layer. When the gateway is unreachable the dialog stays
// visible with its controls disabled and recovers with the connection.
export function Interrupt() {
  const s = state.value;
  if (!needsAttention(s)) return null;
  const secure = s.securePrompts.find((p) => p.kind !== 'touch');
  const [first, ...rest] = s.permissions;
  const disabled = s.conn !== 'live';
  return (
    <div class="interrupt-layer">
      {secure
        ? <SecurePromptDialog prompt={secure} disabled={disabled} />
        : first
          ? <PermissionDialog req={first} more={rest.length} disabled={disabled} />
          : s.question && <QuestionForm req={s.question} disabled={disabled} />}
    </div>
  );
}
