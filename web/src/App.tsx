import AppFrame from './layout/AppFrame'

export default function App() {
  return (
    <AppFrame sidebar={<p>Workspace</p>}>
      <p>No session selected</p>
    </AppFrame>
  )
}
