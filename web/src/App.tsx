import Alert from './app/Alert'
import Unauthorized from './app/Unauthorized'
import Unreachable from './app/Unreachable'
import { useGateway } from './app/useGateway'
import AppFrame from './layout/AppFrame'

export default function App() {
  const gateway = useGateway()

  if (gateway.state === 'authenticating') {
    return <p>Connecting to Crush…</p>
  }
  if (gateway.state === 'unreachable') {
    return <Unreachable message={gateway.error} />
  }
  if (gateway.state === 'unauthorized') {
    return <Unauthorized />
  }

  // Task 15 replaces the sidebar placeholder and Task 16 the conversation
  // one; the error row belongs to the shell and stays.
  return (
    <AppFrame sidebar={<p>Workspace</p>}>
      <Alert store={gateway.store} />
      <p>Select a session</p>
    </AppFrame>
  )
}
