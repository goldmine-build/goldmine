import './index';
import { fetchMock } from 'fetch-mock';
import { $, $$ } from 'common-sk/modules/dom';
import { eventPromise, setUpElementUnderTest } from '../test_util';
import { twoHundredCommits, typicalDetails } from './test_data';

describe('digest-details-sk', () => {
  const newInstance = setUpElementUnderTest('digest-details-sk');

  let digestDetailsSk;
  beforeEach(() => digestDetailsSk = newInstance());

  describe('layout with positive and negative references', () => {
    beforeEach(() => {
      digestDetailsSk.details = typicalDetails;
      digestDetailsSk.commits = twoHundredCommits;
    });

    it('shows the test name', () => {
      expect($$('.top_bar .grouping_name', digestDetailsSk).innerText).to.contain(
        'dots-legend-sk_too-many-digests',
      );
    });

    it('has a link to the cluster view', () => {
      expect($$('a.cluster_link', digestDetailsSk).href).to.contain(
        '/cluster?head=true&limit=200&neg=true&pos=true&query=name%3Ddots-legend-sk_too-many-digests%26source_type%3Dinfra&unt=true',
      );
    });

    it('shows shows both digests', () => {
      const labels = $('.digest_labels .digest_label', digestDetailsSk);
      expect(labels.length).to.equal(2);
      expect(labels[0].innerText).to.contain('6246b773851984c726cb2e1cb13510c2');
      expect(labels[1].innerText).to.contain('99c58c7002073346ff55f446d47d6311');
    });

    it('shows the metrics and the link to the diff page', () => {
      expect($$('.metrics_and_triage a.diffpage_link', digestDetailsSk).href).to.contain(
        '/diff?test=dots-legend-sk_too-many-digests&left=6246b773851984c726cb2e1cb13510c2&right=99c58c7002073346ff55f446d47d6311',
      );

      const metrics = $('.metrics_and_triage .metric', digestDetailsSk).map((e) => e.innerText);
      expect(metrics).to.deep.equal(
        ['Diff metric: 0.083', 'Diff %: 0.22', 'Pixels: 3766', 'Max RGBA: [9,9,9,0]'],
      );

      expect($$('.metrics_and_triage .size_warning', digestDetailsSk).hidden).to.be.true;
    });

    it('has a triage button and shows the triage history', () => {
      expect($$('.metrics_and_triage triage-sk', digestDetailsSk).value).to.equal('positive');
      expect($$('.metrics_and_triage triage-sk', digestDetailsSk).value).to.equal('positive');

      expect($$('.metrics_and_triage triage-history-sk', digestDetailsSk).history.length)
        .to.equal(2);
    });

    it('has an image-compare-sk with the right values', () => {
      const imgComp = $$('.comparison image-compare-sk', digestDetailsSk);
      expect(imgComp.left).to.deep.equal({
        digest: '6246b773851984c726cb2e1cb13510c2',
        title: '6246b7738519...',
        detail: '/detail?test=dots-legend-sk_too-many-digests&digest=6246b773851984c726cb2e1cb13510c2',
      });

      expect(imgComp.right).to.deep.equal({
        digest: '99c58c7002073346ff55f446d47d6311',
        title: 'Closest Positive',
        detail: '/detail?test=dots-legend-sk_too-many-digests&digest=99c58c7002073346ff55f446d47d6311',
      });
    });

    it('changes the reference image when the toggle button is clicked', () => {
      $$('button.toggle_ref', digestDetailsSk).click();

      // Check that the image-comparison shows up
      expect($$('.comparison image-compare-sk', digestDetailsSk).right).to.deep.equal({
        digest: 'ec3b8f27397d99581e06eaa46d6d5837',
        title: 'Closest Negative',
        detail: '/detail?test=dots-legend-sk_too-many-digests&digest=ec3b8f27397d99581e06eaa46d6d5837',
      });

      expect($$('.negative_warning').hidden).to.be.false;
    });

    describe('RPC requests', () => {
      afterEach(() => {
        expect(fetchMock.done()).to.be.true; // All mock RPCs called at least once.
        fetchMock.reset();
      });

      it('POSTs to an RPC endpoint when triage button clicked', async () => {
        const endPromise = eventPromise('end-task');
        fetchMock.post('/json/triage', (url, req) => {
          expect(req.body).to.equal('{"testDigestStatus":{"dots-legend-sk_too-many-digests":{"6246b773851984c726cb2e1cb13510c2":"negative"}}}');
          return 200;
        });

        $$('.metrics_and_triage triage-sk button.negative', digestDetailsSk).click();
        await endPromise;
      });
    });
  });

  describe('layout with changelist id, positive and negative references', () => {
    beforeEach(() => {
      digestDetailsSk.details = typicalDetails;
      digestDetailsSk.commits = twoHundredCommits;
      digestDetailsSk.issue = '12345';
    });

    it('includes changelist id on the appropriate links', () => {
      // (cluster doesn't have issue for now, since that was the way it was done before).
      // TODO(kjlubick) should cluster take changelist ID?
      const imgComp = $$('.comparison image-compare-sk', digestDetailsSk);
      expect(imgComp.left).to.deep.equal({
        digest: '6246b773851984c726cb2e1cb13510c2',
        title: '6246b7738519...',
        detail: '/detail?test=dots-legend-sk_too-many-digests&digest=6246b773851984c726cb2e1cb13510c2&issue=12345',
      });

      expect(imgComp.right).to.deep.equal({
        digest: '99c58c7002073346ff55f446d47d6311',
        title: 'Closest Positive',
        detail: '/detail?test=dots-legend-sk_too-many-digests&digest=99c58c7002073346ff55f446d47d6311&issue=12345',
      });

      expect($$('.metrics_and_triage a.diffpage_link', digestDetailsSk).href).to.contain(
        '/diff?test=dots-legend-sk_too-many-digests&left=6246b773851984c726cb2e1cb13510c2&right=99c58c7002073346ff55f446d47d6311&issue=12345',
      );
    });

    it('passes issue to appropriate subelements', () => {
      expect($$('.trace_info dots-legend-sk', digestDetailsSk).issue).to.equal('12345');
    });

    describe('RPC requests', () => {
      afterEach(() => {
        expect(fetchMock.done()).to.be.true; // All mock RPCs called at least once.
        fetchMock.reset();
      });

      it('includes changelist id when triaging', async () => {
        const endPromise = eventPromise('end-task');
        fetchMock.post('/json/triage', (url, req) => {
          expect(req.body).to.equal('{"testDigestStatus":{"dots-legend-sk_too-many-digests":{"6246b773851984c726cb2e1cb13510c2":"negative"}},"issue":"12345"}');
          return 200;
        });

        $$('.metrics_and_triage triage-sk button.negative', digestDetailsSk).click();
        await endPromise;
      });
    });
  });
});
