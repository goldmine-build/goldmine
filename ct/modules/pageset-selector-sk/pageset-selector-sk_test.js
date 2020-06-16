import './index';

import { $, $$ } from 'common-sk/modules/dom';
import { fetchMock } from 'fetch-mock';
import { pageSets } from './test_data';
import { setUpElementUnderTest } from '../../../infra-sk/modules/test_util';

describe('pageset-selector-sk', () => {
  const factory = setUpElementUnderTest('pageset-selector-sk');
  // Returns a new element with the pagesets fetch complete.
  const newInstance = async (init) => {
    const ele = factory(init);
    await new Promise((resolve) => setTimeout(resolve, 0));
    return ele;
  };

  let selector; // Set at start of each test.
  beforeEach(() => {
    fetchMock.postOnce('begin:/_/page_sets/', pageSets);
  });

  afterEach(() => {
    //  Check all mock fetches called at least once and reset.
    expect(fetchMock.done()).to.be.true;
    fetchMock.reset();
  });

  const simulateUserToggle = () => {
    $$('expandable-textarea-sk > button', selector).click();
  };


  // Simulates a user typing 'value' into the input element by setting its
  // value and triggering the built-in 'input' event.
  const simulateUserEnteringCustomPages = (value) => {
    const ele = $$('textarea', selector);
    ele.focus();
    ele.value = value;
    ele.dispatchEvent(new Event('input', {
      bubbles: true,
      cancelable: true,
    }));
  };

  it('loads selections', async () => {
    selector = await newInstance();
    expect($('select-sk div')).to.have.length(8);
    expect($$('.pageset-list', selector)).to.have.property('hidden', false);
    expect(selector).to.have.property('selected', '100k');
  });

  it('reflects changes to selected', async () => {
    selector = await newInstance();
    expect(selector).to.have.property('selected', '100k');
    $$('select-sk', selector).selection = 3;
    expect(selector).to.have.property('selected', 'Mobile10k');
    selector.selected = 'Dummy1k';
    expect(selector).to.have.property('selected', 'Dummy1k');
    // Invalid keys aren't honored.
    selector.selected = 'bogus key';
    expect(selector).to.have.property('selected', '');
  });

  it('filters out hideIfKeyContains options', async () => {
    selector = await newInstance((ele) => {
      ele.hideIfKeyContains = ['Mobile', '100'];
    });
    expect($('select-sk div')).to.have.length(3);
  });

  it('hides selector when custom page form expanded', async () => {
    selector = await newInstance();
    simulateUserToggle();
    expect($$('.pageset-list', selector)).to.have.property('hidden', true);
  });

  it('clears custom pages when custom page form collapsed ', async () => {
    selector = await newInstance();
    simulateUserToggle();
    simulateUserEnteringCustomPages('example.com');
    expect(selector).to.have.property('customPages', 'example.com');
    expect($$('.pageset-list', selector)).to.have.property('hidden', true);
    simulateUserToggle();
    expect($$('.pageset-list', selector)).to.have.property('hidden', false);
    expect(selector).to.have.property('customPages', '');
  });

  it('disables custom page form on disable-custom-webpages', async () => {
    selector = await newInstance((ele) => {
      ele.setAttribute('disable-custom-webpages', '');
    });
    expect(selector).to.not.contain('expandable-textarea-sk');
    expect(selector).to.contain('select-sk');
  });
});
