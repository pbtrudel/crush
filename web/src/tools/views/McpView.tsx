import { compactInput } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import styles from '../Tools.module.css'
import type { ToolBodyProps, ToolView } from '../view'

// splitMcpName reads mcp_<server>_<tool>. A server name may itself hold an
// underscore, so the longest known server that prefixes the name wins; a
// name no known server explains splits at its first underscore.
export function splitMcpName(name: string, servers: readonly string[]): { server: string; tool: string } {
  const rest = name.startsWith('mcp_') ? name.slice(4) : name
  const known = servers
    .filter((server) => rest.startsWith(`${server}_`))
    .sort((a, b) => b.length - a.length)[0]
  if (known !== undefined) {
    return { server: known, tool: rest.slice(known.length + 1) }
  }
  const split = rest.indexOf('_')
  return split < 0 ? { server: rest, tool: '' } : { server: rest.slice(0, split), tool: rest.slice(split + 1) }
}

// McpBody shows the arguments and the text a server answered with. Both go
// through the output block, which clips what a server can send and keeps
// the reveal across a remount. An image answer is named instead of drawn:
// the client receives the media type but never the payload, so there is no
// address to load. The text beside an image is what the server said about
// it, so it is shown under the note rather than dropped.
function McpBody({ call, input, result }: ToolBodyProps) {
  return (
    <>
      <ToolSection label="Input">
        <OutputBlock
          text={input === null ? call.input : JSON.stringify(input, null, 2)}
          revealKey={`${call.id}:input`}
        />
      </ToolSection>
      {result === undefined ? null : result.mimeType.startsWith('image/') ? (
        <>
          <p className={styles.note}>The server answered with an image ({result.mimeType}).</p>
          {result.content === '' ? null : (
            <OutputBlock text={result.content} revealKey={`${call.id}:output`} />
          )}
        </>
      ) : (
        <ToolSection label={result.isError ? 'Error' : 'Output'}>
          <OutputBlock
            text={result.content}
            tone={result.isError ? 'error' : 'plain'}
            revealKey={result.isError ? `${call.id}:error` : `${call.id}:output`}
          />
        </ToolSection>
      )}
    </>
  )
}

export const mcpView: ToolView = {
  title: (name, servers) => {
    const { server, tool } = splitMcpName(name, servers)
    return tool === '' ? server : `${server} › ${tool}`
  },
  summary: (input) => compactInput(input),
  Body: McpBody,
}
