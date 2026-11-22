import '@fontsource/chakra-petch/500.css';
import '@fontsource/chakra-petch/600.css';
import '@fontsource/ibm-plex-sans/400.css';
import '@fontsource/ibm-plex-sans/500.css';
import '@fontsource/jetbrains-mono/400.css';
import './styles/tokens.css';
import './styles/base.css';
import './styles/shell.css';
import './styles/hud.css';
import './styles/stage.css';
import './styles/log.css';
import './styles/deck.css';
import './styles/interrupt.css';
import './styles/panels.css';
import { render } from 'preact';
import { App } from './ui/App';
import { boot } from './state/store';

render(<App />, document.getElementById('app')!);

// boot() has no double-invocation guard, so it runs here exactly once.
void boot();
