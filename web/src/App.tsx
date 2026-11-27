import Alert from './app/Alert'
import Unauthorized from './app/Unauthorized'
import Unreachable from './app/Unreachable'
import { useGateway } from './app/useGateway'
import AppFrame from './layout/AppFrame'
import SidebarContainer from './sidebar/SidebarContainer'

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

  // Task 16 replaces the conversation placeholder; the sidebar and the error
  // row belong to the shell and stay.
  return (
    <AppFrame sidebar={<SidebarContainer client={gateway.client} store={gateway.store} />}>
      <Alert store={gateway.store} />
      <p>Select a session</p>
    </AppFrame>
  )
}
