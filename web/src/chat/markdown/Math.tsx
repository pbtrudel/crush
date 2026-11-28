import { Suspense, lazy } from 'react'

import MathSource from './MathSource'

export interface MathFormulaProps {
  tex: string
  display: boolean
}

const MathView = lazy(() => import('./MathView'))

// MathFormula shows the TeX source at once and the rendered MathML when
// the lazy KaTeX chunk has loaded. Until it arrives, and for TeX that
// cannot be typeset, the source stands in.
export default function MathFormula({ tex, display }: MathFormulaProps) {
  return (
    <Suspense fallback={<MathSource tex={tex} display={display} />}>
      <MathView tex={tex} display={display} />
    </Suspense>
  )
}
