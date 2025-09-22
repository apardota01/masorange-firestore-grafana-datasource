import React, { ChangeEvent, PureComponent } from 'react';
import {
  QueryField, Button
} from '@grafana/ui';
// import { FieldValues } from "react-hook-form"
import { QueryEditorProps } from '@grafana/data';
import { DataSource } from '../datasource';
import { MyDataSourceOptions, FirestoreQuery } from '../types';

type Props = QueryEditorProps<DataSource, FirestoreQuery, MyDataSourceOptions>;

export class QueryEditor extends PureComponent<Props> {
  timeoutId: NodeJS.Timeout | undefined
  onCollectionChange = (event: ChangeEvent<HTMLInputElement>) => {
    const { onChange, query, onRunQuery } = this.props;
    onChange({ ...query });
    this.runQuery(onRunQuery)
  };

  onQueryChange = (newQuery: string) => {
    const { onChange, query } = this.props;
    onChange({ ...query, query: newQuery});
    // this.runQuery(onRunQuery)
  };

  // Time field removed - users should use $__from and $__to variables in queries

  onRunQuery = () => {
    const { onRunQuery } = this.props;
    onRunQuery();
  }

  runQuery = (onRunQuery: () => void) => {
    if (this.timeoutId) {
      clearTimeout(this.timeoutId)
    }
    this.timeoutId = setTimeout(() => {
      onRunQuery();
      this.timeoutId = undefined
    }, 500)
  }

  onFormSubmit = () => {

  }

  render() {
    const { query } = this.props.query;

    return (
      <div>
        <div className="gf-form">
         <QueryField query={query} placeholder="FireQL query (use $__from and $__to for time filtering)" portalOrigin="" onChange={this.onQueryChange}></QueryField>
         <Button style={{marginLeft: "10px"}} onClick={this.onRunQuery}>Run query</Button>
        </div>
      </div>
    );
  }
}
