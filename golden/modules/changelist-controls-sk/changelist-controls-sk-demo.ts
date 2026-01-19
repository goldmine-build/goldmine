import './index';

import { $$ } from '../../../infra-sk/modules/dom';
import '../../../infra-sk/modules/theme-chooser-sk';
import { ChangelistControlsSk } from './changelist-controls-sk';
import { twoPatchsets } from './test_data';

const ele = $$<ChangelistControlsSk>('changelist-controls-sk');
ele!.summary = twoPatchsets;
