import '@fontsource/chakra-petch/500.css';
import '@fontsource/chakra-petch/600.css';
import '@fontsource/ibm-plex-sans/400.css';
import '@fontsource/ibm-plex-sans/500.css';
import '@fontsource/jetbrains-mono/400.css';
import './styles/tokens.css';
import './styles/base.css';
import { render } from 'preact';
import { App } from './ui/App';

render(<App />, document.getElementById('app')!);
