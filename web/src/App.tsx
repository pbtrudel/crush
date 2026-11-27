import Alert from './app/Alert'
import ConnectionStatus from './app/ConnectionStatus'
import Unauthorized from './app/Unauthorized'
import Unreachable from './app/Unreachable'
import { useGateway } from './app/useGateway'
import ConversationContainer from './conversation/ConversationContainer'
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

  // The sidebar, the error row and the conversation share the frame: the
  // first two belong to the shell, the conversation reads the selected
  // session from the same store.
  return (
    <AppFrame
      sidebar={<SidebarContainer client={gateway.client} store={gateway.store} />}
      status={<ConnectionStatus store={gateway.store} />}
    >
      <Alert store={gateway.store} />
      <ConversationContainer client={gateway.client} store={gateway.store} />
    </AppFrame>
  )
}
