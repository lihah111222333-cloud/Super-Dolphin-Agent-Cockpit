import { errorMessage } from '../../shared/pageShared.js';
import {
  enterpriseOutputTypes,
  enterpriseTemplateCompat,
  enterpriseTemplateFields,
  enterpriseTemplateId,
  requireArrayField,
  requireObjectField,
} from '../services/workflowEnterpriseTemplateModel.js';

export function enterpriseTemplateContractError(template) {
  try {
    enterpriseTemplateId(template);
    enterpriseOutputTypes(template);
    enterpriseTemplateFields(template);
    const compat = enterpriseTemplateCompat(template);
    const dagTemplate = requireObjectField(compat.dagTemplate, 'workflow template dag_template');
    requireArrayField(dagTemplate.nodes, 'workflow template dag_template.nodes');
    requireObjectField(compat.finalOutput, 'workflow template final_output');
    return '';
  } catch (error) {
    return errorMessage(error);
  }
}
