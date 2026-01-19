import { deepCopy } from '../../../infra-sk/modules/object';
import '../../../infra-sk/modules/theme-chooser-sk';
import { clusterDiffJSON } from '../cluster-page-sk/test_data';
import {
  ClusterDiffNodeWithLabel,
  ClusterDigestsSk,
} from './cluster-digests-sk';
import './index';

const clusterDigestsSk = new ClusterDigestsSk();
document.body.querySelector('#cluster')!.appendChild(clusterDigestsSk);

function setData(labels: boolean) {
  const nodes: ClusterDiffNodeWithLabel[] = deepCopy(clusterDiffJSON.nodes!);
  if (labels) {
    nodes.forEach((node, index) => {
      node.label = `node ${index}`;
    });
  }
  clusterDigestsSk.setData(nodes, deepCopy(clusterDiffJSON.links!));
}

setData(false);

const labelsCheckBox = document.querySelector<HTMLInputElement>('#labels')!;
labelsCheckBox.addEventListener('change', (e: Event) => {
  setData(labelsCheckBox.checked);
});
