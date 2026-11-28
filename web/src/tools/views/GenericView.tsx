import { compactInput } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import type { ToolBodyProps, ToolView } from '../view'

function GenericBody({ call, input, result }: ToolBodyProps) {
  return (
    <>
      <ToolSection label="Input">
        <OutputBlock
          text={input === null ? call.input : JSON.stringify(input, null, 2)}
          revealKey={`${call.id}:input`}
        />
      </ToolSection>
      {result ? (
        <ToolSection label={result.isError ? 'Error' : 'Output'}>
          <OutputBlock
            text={result.content}
            tone={result.isError ? 'error' : 'plain'}
            revealKey={`${call.id}:output`}
          />
        </ToolSection>
      ) : null}
    </>
  )
}

// genericView draws any tool no view claims: the raw arguments and the
// text result, so nothing the agent did is hidden.
export const genericView: ToolView = {
  title: (name) => name,
  summary: (input) => compactInput(input),
  Body: GenericBody,
}
