import { needsAttention } from '../../state/reducer';
import { state } from '../../state/store';
import { PermissionDialog } from './PermissionDialog';
import { QuestionForm } from './QuestionForm';

// Interrupt renders whenever App has a reason to inert the shell, so a dialog
// is on screen for as long as the background is locked. A permission request
// wins over a question batch. When the gateway is unreachable the dialog
// stays visible with its controls disabled and recovers with the connection.
export function Interrupt() {
  const s = state.value;
  if (!needsAttention(s)) return null;
  const [first, ...rest] = s.permissions;
  const disabled = s.conn !== 'live';
  return (
    <div class="interrupt-layer">
      {first
        ? <PermissionDialog req={first} more={rest.length} disabled={disabled} />
        : s.question && <QuestionForm req={s.question} disabled={disabled} />}
    </div>
  );
}
